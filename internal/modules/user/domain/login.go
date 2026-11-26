package domain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"app.local/internal/kit/logctx"
)

// ResolveLogin 是"解析账号"的登录分支：所有登录入口都只能经过它。
func (s *Service) ResolveLogin(ctx context.Context, ext ExternalIdentity, client string) (*User, *Identity, error) {
	user, idn, _, err := s.resolveLogin(ctx, ext, client)
	return user, idn, err
}

// resolveLogin 的 created 报告账号是不是这次新建的。
func (s *Service) resolveLogin(ctx context.Context, ext ExternalIdentity, client string) (*User, *Identity, bool, error) {
	idn, err := s.identities.GetByProviderSubject(ctx, ext.Provider, ext.Subject)
	if err != nil && !errors.Is(err, ErrIdentityNotFound) {
		return nil, nil, false, fmt.Errorf("resolve login: %w", err)
	}
	if idn != nil {
		// 状态以 TouchLogin 的条件 UPDATE 为准：先读后判断的话，和停用、注销并发时会给非 active 账号建会话。
		user, err := s.users.TouchLogin(ctx, idn.UserID)
		if errors.Is(err, ErrNotFound) {
			return nil, nil, false, fmt.Errorf("resolve login: %w", ErrAccountUnavailable)
		}
		if err != nil {
			return nil, nil, false, fmt.Errorf("resolve login: %w", err)
		}
		if err := s.identities.TouchLogin(ctx, idn.ID, profileOf(ext)); err != nil {
			return nil, nil, false, fmt.Errorf("resolve login: %w", err)
		}
		if err := s.upsertOpenID(ctx, user.ID, ext); err != nil {
			return nil, nil, false, fmt.Errorf("resolve login: %w", err)
		}
		return user, idn, false, nil
	}

	now := time.Now()
	user := &User{
		ID:           uuid.NewV7().String(),
		DisplayName:  normalizeDisplayName(ext.Name),
		AvatarUrl:    normalizeAvatarURL(ext.AvatarURL),
		Status:       UserStatusActive,
		LastLoginAt:  now,
		SignupClient: client,
	}
	if err := s.users.Create(ctx, user); err != nil {
		return nil, nil, false, fmt.Errorf("resolve login: create user: %w", err)
	}
	idn = &Identity{
		ID:          uuid.NewV7().String(),
		UserID:      user.ID,
		Provider:    ext.Provider,
		Subject:     ext.Subject,
		Profile:     profileOf(ext),
		LastLoginAt: &now,
	}
	if err := s.identities.Create(ctx, idn); err != nil {
		return nil, nil, false, fmt.Errorf("resolve login: create identity: %w", err)
	}
	if err := s.upsertOpenID(ctx, user.ID, ext); err != nil {
		return nil, nil, false, fmt.Errorf("resolve login: %w", err)
	}
	return user, idn, true, nil
}

// Login 在同一个事务里解析账号和建会话，access token 在事务外签发。登记了会话策略时，事务前先准备策略，
// 事务里按策略顶掉旧会话（admitSession）。
func (s *Service) Login(ctx context.Context, lc LoginClient, ext ExternalIdentity) (*IssuedTokens, error) {
	prep, err := s.prepareSessionPolicy(ctx, lc, ext)
	if err != nil {
		return nil, err
	}
	var sess *Session
	var refreshRandom string
	err = s.uow.Run(ctx, func(ctx context.Context) error {
		user, idn, created, err := s.resolveLogin(ctx, ext, lc.Client)
		if err != nil {
			return err
		}
		if err := s.admitSession(ctx, prep, user.ID, created, lc); err != nil {
			return err
		}
		sess, refreshRandom, err = s.newSession(ctx, user.ID, &idn.ID, lc, ext.Provider)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.issueTokens(sess, refreshRandom, "")
}

// preparedPolicy 是事务前准备好的会话策略。userID 为空表示准备时账号还不存在（首次登录）。
type preparedPolicy struct {
	enabled bool
	userID  string
	decide  Decide
}

// prepareSessionPolicy 在事务之前按登录方式预查账号并调用策略的 Prepare：策略可能要读其它模块，跨模块读不能放进
// 事务（见 docs/agents/modules.md）。账号还不存在时没有旧会话，不需要准备。
func (s *Service) prepareSessionPolicy(ctx context.Context, lc LoginClient, ext ExternalIdentity) (preparedPolicy, error) {
	if s.policy == nil {
		return preparedPolicy{}, nil
	}
	idn, err := s.identities.GetByProviderSubject(ctx, ext.Provider, ext.Subject)
	if errors.Is(err, ErrIdentityNotFound) {
		return preparedPolicy{enabled: true}, nil
	}
	if err != nil {
		return preparedPolicy{}, fmt.Errorf("login: prepare session policy: %w", err)
	}
	decide, err := s.policy.Prepare(ctx, lc.candidate(idn.UserID))
	if err != nil {
		return preparedPolicy{}, fmt.Errorf("login: prepare session policy: %w", err)
	}
	return preparedPolicy{enabled: true, userID: idn.UserID, decide: decide}, nil
}

// admitSession 在登录事务里执行策略：resolveLogin 的 TouchLogin 已锁住用户行，同一账号的并发登录在这里排队，
// 列出的有效会话包含先提交的那次登录。
func (s *Service) admitSession(ctx context.Context, prep preparedPolicy, userID string, created bool, lc LoginClient) error {
	if !prep.enabled {
		return nil
	}
	if prep.userID != userID {
		if prep.userID == "" && created {
			return nil
		}
		// 准备之后、加锁之前登录方式换了账号（并发的首次登录或换绑）：准备的数据不属于这个账号，让客户端重试。
		s.log().WarnContext(logctx.WithUserID(ctx, userID), "user: login identity moved between policy prepare and login, retry")
		return fmt.Errorf("login: %w", ErrLoginFailed)
	}
	if prep.decide == nil {
		return nil
	}
	sessions, err := s.sessions.ListActiveByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("login: list active sessions: %w", err)
	}
	active := make([]ActiveSession, 0, len(sessions))
	known := make(map[string]bool, len(sessions))
	for _, v := range sessions {
		active = append(active, ActiveSession{
			ID: v.ID, Client: v.Client, DeviceClass: v.DeviceClass, DeviceName: v.DeviceName,
			CreatedAt: v.CreatedAt, LastUsedAt: v.LastUsedAt,
		})
		known[v.ID] = true
	}
	revoke, err := prep.decide(lc.candidate(userID), active)
	if err != nil {
		return fmt.Errorf("login: session policy: %w", err)
	}
	for _, id := range revoke {
		if !known[id] {
			return fmt.Errorf("login: session policy returned session %q that is not an active session of the user", id)
		}
	}
	if len(revoke) == 0 {
		return nil
	}
	if err := s.sessions.RevokeReplaced(ctx, userID, revoke); err != nil {
		return fmt.Errorf("login: revoke replaced sessions: %w", err)
	}
	return nil
}

func (lc LoginClient) candidate(userID string) SessionCandidate {
	return SessionCandidate{UserID: userID, Client: lc.Client, DeviceClass: lc.DeviceClass, DeviceName: lc.DeviceName}
}

// Reauthenticate 校验第三方身份属于当前账号，成功后更新会话的 auth_time 和 amr。
func (s *Service) Reauthenticate(ctx context.Context, sid string, ext ExternalIdentity) error {
	return s.uow.Run(ctx, func(ctx context.Context) error {
		sess, err := s.validSession(ctx, sid)
		if err != nil {
			return fmt.Errorf("reauthenticate: %w", err)
		}
		idn, err := s.identities.GetByProviderSubject(ctx, ext.Provider, ext.Subject)
		if errors.Is(err, ErrIdentityNotFound) {
			return fmt.Errorf("reauthenticate: %w", ErrReauthWrongAccount)
		}
		if err != nil {
			return fmt.Errorf("reauthenticate: %w", err)
		}
		if idn.UserID != sess.UserID {
			return fmt.Errorf("reauthenticate: %w", ErrReauthWrongAccount)
		}
		return s.sessions.UpdateAuthTime(ctx, sid, time.Now(), ext.Provider)
	})
}

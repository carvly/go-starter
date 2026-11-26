package domain

import (
	"log/slog"

	"app.local/internal/kit/uow"
)

type Service struct {
	users      Repository
	identities IdentityRepository
	openIDs    WeChatOpenIDRepository
	sessions   SessionRepository
	states     OAuthStateRepository
	publisher  Publisher
	issuer     AccessTokenIssuer
	uow        uow.UnitOfWork
	logger     *slog.Logger
	cfg        Config
	policy     SessionPolicy
}

func NewService(d Deps) *Service {
	return &Service{
		users:      d.Users,
		identities: d.Identities,
		openIDs:    d.OpenIDs,
		sessions:   d.Sessions,
		states:     d.States,
		publisher:  d.Publisher,
		issuer:     d.Issuer,
		uow:        d.UoW,
		logger:     d.Logger,
		cfg:        d.Config,
		policy:     d.SessionPolicy,
	}
}

// SetSessionPolicy 在启动时登记会话策略。只能有一个：已经有策略（配置启用了内置策略，或重复登记）时 panic，
// 多个策略的先后没有自然定义，需要组合的在一个实现里组合。
func (s *Service) SetSessionPolicy(p SessionPolicy) {
	if p == nil {
		panic("user: SetSessionPolicy: policy is nil")
	}
	if s.policy != nil {
		panic("user: SetSessionPolicy: a session policy is already set (USER_SESSION_SLOT_LIMIT or an earlier SetSessionPolicy)")
	}
	s.policy = p
}

func (s *Service) log() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

const (
	DefaultListLimit = 20
	MaxListLimit     = 100
)

func normalizeLimit(limit int) int {
	if limit <= 0 || limit > MaxListLimit {
		return DefaultListLimit
	}
	return limit
}

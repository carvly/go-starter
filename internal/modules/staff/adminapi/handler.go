package adminapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/kit/logctx"
	"app.local/internal/modules/staff/adminapi/oapi"
	staff "app.local/internal/modules/staff/domain"
	"app.local/internal/platform/httpserver"
)

type Handler struct {
	svc *staff.Service
	// sessionMaxAge 是会话的绝对时长，也是 cookie 的 Max-Age。
	sessionMaxAge time.Duration
}

func NewHandler(svc *staff.Service, sessionMaxAge time.Duration) *Handler {
	return &Handler{svc: svc, sessionMaxAge: sessionMaxAge}
}

// principal 取出 Guard 写入的当前员工。
func principal(c *echo.Context) (*staff.Principal, error) {
	s, err := httpserver.StaffSessionFrom(c)
	if err != nil {
		return nil, err
	}
	return &staff.Principal{StaffID: s.StaffID, SessionID: s.SessionID, SuperAdmin: s.SuperAdmin, MustChangePassword: s.MustChangePassword}, nil
}

func bind[T any](c *echo.Context) (*T, error) {
	var req T
	if err := c.Bind(&req); err != nil {
		return nil, err
	}
	if err := c.Validate(&req); err != nil {
		return nil, err
	}
	return &req, nil
}

func (h *Handler) setSessionCookie(c *echo.Context, token string, maxAge time.Duration) {
	c.SetCookie(&http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handler) StaffLogin(c *echo.Context) error {
	req, err := bind[oapi.StaffLoginRequest](c)
	if err != nil {
		return err
	}
	token, a, err := h.svc.Login(c.Request().Context(), staff.LoginInput{
		Username:  req.Username,
		Password:  req.Password,
		IP:        c.RealIP(),
		UserAgent: c.Request().UserAgent(),
		RequestID: logctx.RequestID(c.Request().Context()),
	})
	if err != nil {
		return err
	}
	h.setSessionCookie(c, token, h.sessionMaxAge)
	return c.JSON(http.StatusOK, toMe(a))
}

func (h *Handler) StaffLogout(c *echo.Context) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := h.svc.Logout(c.Request().Context(), p); err != nil {
		return err
	}
	c.SetCookie(&http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) GetMe(c *echo.Context) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	a, err := h.svc.GetAccount(c.Request().Context(), p.StaffID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toMe(a))
}

func (h *Handler) GetMyPermissions(c *echo.Context) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	perms, err := h.svc.Permissions(c.Request().Context(), p)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, oapi.MyPermissions{Permissions: perms})
}

func (h *Handler) ChangeMyPassword(c *echo.Context) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	req, err := bind[oapi.ChangePasswordRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.ChangePassword(c.Request().Context(), p, req.CurrentPassword, req.NewPassword); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) ListMySessions(c *echo.Context) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	sessions, err := h.svc.ListSessions(c.Request().Context(), p.StaffID)
	if err != nil {
		return err
	}
	resp := oapi.SessionList{Items: make([]oapi.Session, 0, len(sessions))}
	for _, s := range sessions {
		resp.Items = append(resp.Items, oapi.Session{
			Id: s.ID, CreatedAt: s.CreatedAt, LastUsedAt: s.LastUsedAt, ExpiresAt: s.ExpiresAt,
			RevokedAt: s.RevokedAt, Ip: s.IP, UserAgent: s.UserAgent, Current: s.ID == p.SessionID,
			Status: oapi.SessionStatus(s.Status),
		})
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *Handler) RevokeMySession(c *echo.Context, id oapi.ID) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := h.svc.RevokeSession(c.Request().Context(), p.StaffID, id.String()); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) ListAccounts(c *echo.Context, params oapi.ListAccountsParams) error {
	ctx := c.Request().Context()
	items, next, err := h.svc.ListAccounts(ctx, deref(params.Cursor), deref(params.Limit))
	if err != nil {
		return err
	}
	ids := make([]string, len(items))
	for i, a := range items {
		ids[i] = a.ID
	}
	roles, err := h.svc.AccountRoleIDs(ctx, ids)
	if err != nil {
		return err
	}
	resp := oapi.AccountList{Items: make([]oapi.AccountDetail, 0, len(items))}
	// 数量与分页无关，只在第一页返回，翻页时不重复查询。
	if deref(params.Cursor) == "" {
		supers, err := h.svc.CountActiveSuperAdmins(ctx)
		if err != nil {
			return err
		}
		resp.ActiveSuperAdmins = &supers
	}
	for _, a := range items {
		resp.Items = append(resp.Items, toAccountDetail(a, roles))
	}
	if next != "" {
		resp.NextCursor = &next
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *Handler) CreateAccount(c *echo.Context) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	req, err := bind[oapi.CreateAccountRequest](c)
	if err != nil {
		return err
	}
	a, temp, err := h.svc.CreateAccount(c.Request().Context(), staff.CreateAccountInput{
		ActorID: p.StaffID, Username: req.Username, DisplayName: deref(req.DisplayName), SuperAdmin: deref(req.SuperAdmin),
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, oapi.CreatedAccount{Account: toAccount(a), TemporaryPassword: temp, TemporaryPasswordExpiresAt: *a.TempPasswordExpiresAt})
}

func (h *Handler) GetAccount(c *echo.Context, id oapi.ID) error {
	ctx := c.Request().Context()
	a, err := h.svc.GetAccount(ctx, id.String())
	if err != nil {
		return err
	}
	roles, err := h.svc.AccountRoleIDs(ctx, []string{a.ID})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toAccountDetail(a, roles))
}

func (h *Handler) DisableAccount(c *echo.Context, id oapi.ID) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := h.svc.Disable(c.Request().Context(), p.StaffID, id.String()); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) EnableAccount(c *echo.Context, id oapi.ID) error {
	if err := h.svc.Enable(c.Request().Context(), id.String()); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) ResetAccountPassword(c *echo.Context, id oapi.ID) error {
	temp, expires, err := h.svc.ResetPassword(c.Request().Context(), id.String())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, oapi.TemporaryPassword{TemporaryPassword: temp, TemporaryPasswordExpiresAt: expires})
}

func (h *Handler) RevokeAccountSessions(c *echo.Context, id oapi.ID) error {
	if err := h.svc.RevokeSessions(c.Request().Context(), id.String()); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) SetAccountSuperAdmin(c *echo.Context, id oapi.ID) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	req, err := bind[oapi.SetSuperAdminRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.SetSuperAdmin(c.Request().Context(), p.StaffID, id.String(), req.SuperAdmin); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) SetAccountRoles(c *echo.Context, id oapi.ID) error {
	req, err := bind[oapi.SetAccountRolesRequest](c)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(req.RoleIds))
	for _, r := range req.RoleIds {
		ids = append(ids, strings.ToLower(r))
	}
	if err := h.svc.BindRoles(c.Request().Context(), id.String(), ids); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) ListRoles(c *echo.Context) error {
	roles, err := h.svc.ListRoles(c.Request().Context())
	if err != nil {
		return err
	}
	resp := oapi.RoleList{Items: make([]oapi.Role, 0, len(roles))}
	for _, r := range roles {
		resp.Items = append(resp.Items, toRole(r))
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *Handler) CreateRole(c *echo.Context) error {
	req, err := bind[oapi.RoleRequest](c)
	if err != nil {
		return err
	}
	r, err := h.svc.CreateRole(c.Request().Context(), req.Name, deref(req.Description))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, toRole(r))
}

func (h *Handler) UpdateRole(c *echo.Context, id oapi.ID) error {
	req, err := bind[oapi.RoleRequest](c)
	if err != nil {
		return err
	}
	r, err := h.svc.UpdateRole(c.Request().Context(), id.String(), req.Name, deref(req.Description))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toRole(r))
}

func (h *Handler) DeleteRole(c *echo.Context, id oapi.ID) error {
	if err := h.svc.DeleteRole(c.Request().Context(), id.String()); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) SetRolePermissions(c *echo.Context, id oapi.ID) error {
	req, err := bind[oapi.SetRolePermissionsRequest](c)
	if err != nil {
		return err
	}
	r, err := h.svc.SetRolePermissions(c.Request().Context(), id.String(), req.Permissions)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toRole(r))
}

func (h *Handler) ListPermissions(c *echo.Context) error {
	cat := h.svc.Catalog()
	resp := oapi.PermissionList{Items: make([]oapi.Permission, 0, len(cat))}
	for _, p := range cat {
		resp.Items = append(resp.Items, oapi.Permission{Code: p.Code, Tag: p.Tag, TagSummary: p.TagSummary, Operations: p.Operations})
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *Handler) ListAuditLogs(c *echo.Context, params oapi.ListAuditLogsParams) error {
	f := staff.AuditFilter{Cursor: deref(params.Cursor), Limit: deref(params.Limit)}
	if params.StaffId != nil {
		f.StaffID = params.StaffId.String()
	}
	if params.From != nil {
		f.From = *params.From
	}
	if params.To != nil {
		f.To = *params.To
	}
	items, next, err := h.svc.QueryAudit(c.Request().Context(), f)
	if err != nil {
		return err
	}
	resp := oapi.AuditLogList{Items: make([]oapi.AuditLog, 0, len(items))}
	for _, e := range items {
		resp.Items = append(resp.Items, oapi.AuditLog{
			Id: e.ID, StaffId: optional(e.StaffID), Action: e.Action, Target: e.Target,
			Result: e.Result, Ip: e.IP, RequestId: e.RequestID, CreatedAt: e.CreatedAt,
		})
	}
	if next != "" {
		resp.NextCursor = &next
	}
	return c.JSON(http.StatusOK, resp)
}

func toMe(a *staff.Account) oapi.Me {
	return oapi.Me{Id: a.ID, Username: a.Username, DisplayName: a.DisplayName, SuperAdmin: a.SuperAdmin, MustChangePassword: a.MustChangePassword}
}

func toAccount(a *staff.Account) oapi.Account {
	return oapi.Account{
		Id: a.ID, Username: a.Username, DisplayName: a.DisplayName, SuperAdmin: a.SuperAdmin,
		MustChangePassword: a.MustChangePassword, Status: oapi.AccountStatus(a.Status),
		CreatedBy: optional(a.CreatedBy), CreatedAt: a.CreatedAt,
	}
}

// toAccountDetail 带上员工的角色 ID；没有角色时是空数组而不是 null。
func toAccountDetail(a *staff.Account, roles map[string][]string) oapi.AccountDetail {
	acc := toAccount(a)
	return oapi.AccountDetail{
		Id: acc.Id, Username: acc.Username, DisplayName: acc.DisplayName, SuperAdmin: acc.SuperAdmin,
		MustChangePassword: acc.MustChangePassword, Status: oapi.AccountDetailStatus(acc.Status),
		CreatedBy: acc.CreatedBy, CreatedAt: acc.CreatedAt, RoleIds: append([]string{}, roles[a.ID]...),
	}
}

func toRole(r *staff.Role) oapi.Role {
	return oapi.Role{Id: r.ID, Name: r.Name, Description: r.Description, Permissions: r.Permissions, CreatedAt: r.CreatedAt}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

var _ oapi.ServerInterface = (*Handler)(nil)

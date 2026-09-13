// Package user 实现用户、角色与外部 API Key 管理。
//
// 角色模型（对齐文书）：
//
//	super_admin：全部权限 + 系统配置
//	admin：用户管理、套餐、数据查看
//	user：自己的信号、持仓、订阅
//
// 安全：密码使用 bcrypt 哈希；API Key 使用 crypto/rand 生成，且只在创建时返回一次。
package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// Role 角色。
type Role string

const (
	RoleSuperAdmin Role = "super_admin"
	RoleAdmin      Role = "admin"
	RoleUser       Role = "user"
)

// User 用户模型。
type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	Status       string    `json:"status"`
	ReferralCode string    `json:"referral_code"`
	ReferredBy   *int64    `json:"referred_by,omitempty"`
	APIKey       string    `json:"api_key,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Service 用户服务。
type Service struct {
	pool      *pgxpool.Pool
	jwtSecret string
	jwtTTL    time.Duration
}

// NewService 构建用户服务。
func NewService(pool *pgxpool.Pool, jwtSecret string, jwtTTLHours int) *Service {
	if jwtTTLHours <= 0 {
		jwtTTLHours = 168
	}
	return &Service{pool: pool, jwtSecret: jwtSecret, jwtTTL: time.Duration(jwtTTLHours) * time.Hour}
}

// Create 注册新用户（返回用户，API Key 只在本次返回）。
func (s *Service) Create(ctx context.Context, email, password, referredByCode string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, errors.New("user: 邮箱格式不正确")
	}
	if len(password) < 8 {
		return nil, errors.New("user: 密码至少 8 位")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	apiKey, err := randomKey(32)
	if err != nil {
		return nil, err
	}
	referral, err := randomKey(4)
	if err != nil {
		return nil, err
	}

	var referredBy *int64
	if strings.TrimSpace(referredByCode) != "" {
		var id int64
		err := s.pool.QueryRow(ctx, `SELECT id FROM users WHERE referral_code = $1`, strings.TrimSpace(referredByCode)).Scan(&id)
		if err == nil {
			referredBy = &id
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}

	var u User
	err = s.pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, referral_code, referred_by, api_key)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, email, role, status, referral_code, referred_by, api_key, created_at, updated_at`,
		email, string(hash), string(RoleUser), referral, referredBy, apiKey,
	).Scan(&u.ID, &u.Email, &u.Role, &u.Status, &u.ReferralCode, &u.ReferredBy, &u.APIKey, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return nil, errors.New("user: 该邮箱已注册")
		}
		return nil, err
	}
	return &u, nil
}

// Authenticate 校验邮箱密码。
func (s *Service) Authenticate(ctx context.Context, email, password string) (*User, error) {
	u, err := s.getByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, errors.New("user: 邮箱或密码错误")
	}
	if u.Status != "active" {
		return nil, errors.New("user: 账号已被禁用")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, errors.New("user: 邮箱或密码错误")
	}
	return u, nil
}

// Login 登录并签发令牌。
func (s *Service) Login(ctx context.Context, email, password string) (string, *User, error) {
	u, err := s.Authenticate(ctx, email, password)
	if err != nil {
		return "", nil, err
	}
	token, err := SignToken(s.jwtSecret, Claims{UserID: u.ID, Role: string(u.Role)}, s.jwtTTL)
	if err != nil {
		return "", nil, err
	}
	return token, u, nil
}

// GetByID 按 ID 查询用户。
func (s *Service) GetByID(ctx context.Context, id int64) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, role, status, referral_code, referred_by, COALESCE(api_key,''), created_at, updated_at
		  FROM users WHERE id = $1`, id).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.Status, &u.ReferralCode, &u.ReferredBy, &u.APIKey, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByAPIKey 按 API Key 查询用户（外部 API 鉴权）。
func (s *Service) GetByAPIKey(ctx context.Context, apiKey string) (*User, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("user: 缺少 API Key")
	}
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, role, status, referral_code, api_key
		  FROM users WHERE api_key = $1`, apiKey).
		Scan(&u.ID, &u.Email, &u.Role, &u.Status, &u.ReferralCode, &u.APIKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("user: API Key 无效")
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// RotateAPIKey 重新生成 API Key（旧 Key 立即失效）。
func (s *Service) RotateAPIKey(ctx context.Context, userID int64) (string, error) {
	key, err := randomKey(32)
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `UPDATE users SET api_key = $2, updated_at = NOW() WHERE id = $1`, userID, key)
	if err != nil {
		return "", err
	}
	return key, nil
}

// List 分页查询用户（管理端）。
func (s *Service) List(ctx context.Context, limit, offset int) ([]*User, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, email, role, status, referral_code, referred_by, created_at
		  FROM users ORDER BY id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.Status, &u.ReferralCode, &u.ReferredBy, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &u)
	}
	return out, rows.Err()
}

// SetStatus 启用/禁用用户（管理端）。
func (s *Service) SetStatus(ctx context.Context, userID int64, status string) error {
	if status != "active" && status != "banned" {
		return fmt.Errorf("user: 非法状态 %q", status)
	}
	_, err := s.pool.Exec(ctx, `UPDATE users SET status = $2, updated_at = NOW() WHERE id = $1`, userID, status)
	return err
}

// SetRole 调整角色（仅 super_admin 可用，权限校验在 API 层完成）。
func (s *Service) SetRole(ctx context.Context, userID int64, role Role) error {
	switch role {
	case RoleSuperAdmin, RoleAdmin, RoleUser:
	default:
		return fmt.Errorf("user: 非法角色 %q", role)
	}
	_, err := s.pool.Exec(ctx, `UPDATE users SET role = $2, updated_at = NOW() WHERE id = $1`, userID, string(role))
	return err
}

func (s *Service) getByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, role, status, referral_code, referred_by, COALESCE(api_key,''), created_at, updated_at
		  FROM users WHERE email = $1`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.Status, &u.ReferralCode, &u.ReferredBy, &u.APIKey, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func randomKey(nbytes int) (string, error) {
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

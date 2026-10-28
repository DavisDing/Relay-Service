package storage

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PostgresConnection is used only for an explicit administrator-requested check.
// Never persist, log or serialize it in a response.
type PostgresConnection struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
	SSLMode  string `json:"sslmode"`
}

type DatabaseCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type PostgresPreflight struct {
	Ready  bool            `json:"ready"`
	Checks []DatabaseCheck `json:"checks"`
}

// PreflightPostgres performs bounded, read-only checks against one explicitly
// supplied destination. It never creates roles, schemas, tables or a database,
// takes migration locks, copies data or changes the active Relay backend.
// Ready means only that these prerequisites passed, not migration acceptance.
func PreflightPostgres(ctx context.Context, input PostgresConnection) PostgresPreflight {
	out := PostgresPreflight{Ready: true, Checks: []DatabaseCheck{}}
	add := func(name, status, message string) {
		out.Checks = append(out.Checks, DatabaseCheck{name, status, message})
		if status == "failed" {
			out.Ready = false
		}
	}
	input.Host = strings.TrimSpace(input.Host)
	if !validPostgresHost(input.Host) || input.Port < 1 || input.Port > 65535 ||
		input.Database == "" || len(input.Database) > 63 || input.Username == "" || len(input.Username) > 63 ||
		strings.ContainsAny(input.Database+input.Username+input.Password, "\x00") ||
		input.Password == "" || len(input.Password) > 1024 ||
		(input.SSLMode != "disable" && input.SSLMode != "require" && input.SSLMode != "verify-full") {
		add("input", "failed", "请填写主机、1～65535 的端口、数据库、用户名、密码及有效的 TLS 模式。")
		return out
	}
	switch strings.ToLower(input.Database) {
	case "postgres", "template0", "template1":
		add("database", "failed", "请使用 Relay 专用数据库，不要连接 PostgreSQL 的管理库或模板库。")
		return out
	}
	u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(input.Host, fmt.Sprint(input.Port)), Path: "/" + input.Database, User: url.UserPassword(input.Username, input.Password)}
	query := u.Query()
	query.Set("sslmode", input.SSLMode)
	u.RawQuery = query.Encode()
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		add("input", "failed", "连接参数无效，请核对主机、数据库及 TLS 设置。")
		return out
	}
	// Explicit single-host credentials and options take precedence over libpq
	// environment defaults. No alternate destinations or plaintext TLS fallback.
	cfg.Host, cfg.Port = input.Host, uint16(input.Port)
	cfg.Database, cfg.User, cfg.Password = input.Database, input.Username, input.Password
	cfg.Fallbacks = nil
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams = map[string]string{"application_name": "relay-database-preflight", "default_transaction_read_only": "on"}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		add("connection", "failed", postgresPreflightMessage(err))
		return out
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = conn.Close(closeCtx)
	}()
	add("connection", "passed", "已连接到指定数据库，账号与密码验证通过。")
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		add("inspection", "failed", postgresPreflightMessage(err))
		return out
	}
	defer tx.Rollback(ctx)
	var schema string
	var schemaExists, usage, create, superuser bool
	err = tx.QueryRow(ctx, `SELECT COALESCE(current_schema(), ''),
EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='public'),
CASE WHEN to_regnamespace('public') IS NULL THEN false ELSE has_schema_privilege('public','USAGE') END,
CASE WHEN to_regnamespace('public') IS NULL THEN false ELSE has_schema_privilege('public','CREATE') END,
(SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&schema, &schemaExists, &usage, &create, &superuser)
	if err != nil {
		add("permissions", "failed", postgresPreflightMessage(err))
		return out
	}
	if superuser {
		add("account", "failed", "当前账号是 PostgreSQL 超级用户。请先创建 Relay 专用普通账号，避免将管理员账号用于 Relay 日常运行。")
	} else {
		add("account", "passed", "使用普通数据库账号。请确认该账号仅用于 Relay。")
	}
	if !schemaExists || !usage || !create || schema != "public" {
		add("permissions", "failed", "需要 public schema 的 USAGE、CREATE 权限，且默认 schema 为 public。请按下方说明在 Relay 专用库中授权；本检查不会修改权限。")
		return out
	}
	add("permissions", "passed", "具备 public schema 的访问与建表权限。")
	var hasObjects bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='public' AND c.relkind IN ('r','p','f','S','v','m'))`).Scan(&hasObjects)
	if err != nil {
		add("target", "failed", postgresPreflightMessage(err))
		return out
	}
	if hasObjects {
		// Existing objects require full migration/schema verification; this
		// lightweight read-only check deliberately does not certify them.
		add("target", "failed", "public schema 已有表、序列或视图。请使用新的 Relay 专用空库；不要删除或覆盖现有对象。已有 Relay 库应走原部署或迁移恢复流程。")
		return out
	}
	add("target", "passed", "public schema 未发现表、序列或视图，可作为新部署的候选目标。")
	add("scope", "passed", "只读前置检查已完成，未建库、建表、保存密码、迁移数据或切换数据库。实际初始化仍需检查完整结构与权限。")
	return out
}

func validPostgresHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast()
	}
	for _, r := range host {
		if r > unicode.MaxASCII || !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// Only fixed messages reach HTTP clients. pgx and PostgreSQL diagnostics may
// contain a connection string, role, server-side detail or other credentials.
func postgresPreflightMessage(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "3D000":
			return "指定数据库不存在。请先由 PostgreSQL 管理员创建 Relay 专用库，再重新检查。"
		case "28P01", "28000":
			return "数据库认证失败。请核对专用账号、密码以及 PostgreSQL 的连接授权。"
		case "42501":
			return "数据库权限不足。请让管理员核对 CONNECT、public schema 的 USAGE 和 CREATE 权限。"
		case "53300", "57P03":
			return "PostgreSQL 暂时无法接受连接，请稍后重试。"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "检查超时或已取消。请核对网络、主机名称和端口后重试。"
	}
	return "无法完成数据库检查。请核对网络、主机、端口、TLS 证书及数据库授权；同网络容器应使用服务名和容器内端口。"
}

package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/guohai/server-status/internal/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUnauthorized    = errors.New("unauthorized")
	ErrNotFound        = errors.New("not found")
	ErrInvalidResource = errors.New("metric references an inactive inventory resource")
)

const (
	machineTypeLabelKey = "server_status.machine_type"
	primaryIPLabelKey   = "server_status.primary_ip"
)

type Store struct {
	pool *pgxpool.Pool
}

type NodeAuth struct {
	TokenID string
	NodeID  string
	AgentID string
}

type RegisterNodeInput struct {
	AgentID      string            `json:"agent_id,omitempty"`
	Hostname     string            `json:"hostname"`
	DisplayName  string            `json:"display_name,omitempty"`
	OSName       string            `json:"os_name,omitempty"`
	OSVersion    string            `json:"os_version,omitempty"`
	Architecture string            `json:"architecture,omitempty"`
	AgentVersion string            `json:"agent_version,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

type NodeCredentials struct {
	NodeID  string `json:"node_id"`
	AgentID string `json:"agent_id"`
	Token   string `json:"token"`
}

func New(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 10 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
	config.ConnConfig.RuntimeParams["timezone"] = "UTC"
	config.ConnConfig.RuntimeParams["application_name"] = "server-status-central"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	result := &Store{pool: pool}
	if err := result.Ready(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return result, nil
}

func (store *Store) Close() {
	store.pool.Close()
}

func (store *Store) Ready(ctx context.Context) error {
	var exists bool
	err := store.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM monitoring.schema_migrations WHERE version = 'V007'
		)
	`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check database schema: %w", err)
	}
	if !exists {
		return errors.New("database schema migration V007 is not installed")
	}
	return nil
}

func (store *Store) AuthenticateToken(ctx context.Context, rawToken string) (NodeAuth, error) {
	digest := sha256.Sum256([]byte(rawToken))
	var auth NodeAuth
	err := store.pool.QueryRow(ctx, `
		SELECT token.id::text, node.id::text, node.agent_id::text
		  FROM monitoring.node_api_tokens token
		  JOIN monitoring.nodes node ON node.id = token.node_id
		 WHERE token.token_digest = $1
		   AND token.revoked_at IS NULL
		   AND (token.expires_at IS NULL OR token.expires_at > CURRENT_TIMESTAMP)
		   AND node.disabled_at IS NULL
	`, digest[:]).Scan(&auth.TokenID, &auth.NodeID, &auth.AgentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeAuth{}, ErrUnauthorized
	}
	if err != nil {
		return NodeAuth{}, fmt.Errorf("authenticate node token: %w", err)
	}
	return auth, nil
}

func (store *Store) RegisterNode(ctx context.Context, input RegisterNodeInput) (NodeCredentials, error) {
	if input.AgentID == "" {
		generated, err := id.NewUUID()
		if err != nil {
			return NodeCredentials{}, err
		}
		input.AgentID = generated
	}
	if input.Hostname == "" {
		input.Hostname = "pending-registration"
	}
	if input.OSName == "" {
		input.OSName = "unknown"
	}
	if input.Architecture == "" {
		input.Architecture = "unknown"
	}
	if input.AgentVersion == "" {
		input.AgentVersion = "pending"
	}
	if input.Labels == nil {
		input.Labels = map[string]string{}
	}
	labels, err := json.Marshal(input.Labels)
	if err != nil {
		return NodeCredentials{}, fmt.Errorf("encode labels: %w", err)
	}
	rawTokenBytes := make([]byte, 32)
	if _, err := rand.Read(rawTokenBytes); err != nil {
		return NodeCredentials{}, fmt.Errorf("generate node token: %w", err)
	}
	rawToken := base64.RawURLEncoding.EncodeToString(rawTokenBytes)
	digest := sha256.Sum256([]byte(rawToken))
	prefix := rawToken
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return NodeCredentials{}, fmt.Errorf("begin registration: %w", err)
	}
	defer tx.Rollback(ctx)
	var nodeID string
	err = tx.QueryRow(ctx, `
		INSERT INTO monitoring.nodes (
			agent_id, hostname, display_name, os_name, os_version,
			architecture, agent_version, labels
		) VALUES ($1::uuid, $2, NULLIF($3, ''), $4, NULLIF($5, ''), $6, $7, $8::jsonb)
		ON CONFLICT (agent_id) DO UPDATE SET
			hostname = EXCLUDED.hostname,
			display_name = COALESCE(EXCLUDED.display_name, monitoring.nodes.display_name),
			os_name = EXCLUDED.os_name,
			os_version = COALESCE(EXCLUDED.os_version, monitoring.nodes.os_version),
			architecture = EXCLUDED.architecture,
			agent_version = EXCLUDED.agent_version,
			labels = EXCLUDED.labels,
			updated_at = CURRENT_TIMESTAMP,
			disabled_at = NULL
		RETURNING id::text
	`, input.AgentID, input.Hostname, input.DisplayName, input.OSName, input.OSVersion, input.Architecture, input.AgentVersion, labels).Scan(&nodeID)
	if err != nil {
		return NodeCredentials{}, fmt.Errorf("upsert node registration: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE monitoring.node_api_tokens
		   SET revoked_at = CURRENT_TIMESTAMP
		 WHERE node_id = $1::uuid AND revoked_at IS NULL
	`, nodeID); err != nil {
		return NodeCredentials{}, fmt.Errorf("revoke previous node tokens: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO monitoring.node_api_tokens (node_id, token_prefix, token_digest, label)
		VALUES ($1::uuid, $2, $3, 'admin registration')
	`, nodeID, prefix, digest[:]); err != nil {
		return NodeCredentials{}, fmt.Errorf("insert node token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return NodeCredentials{}, fmt.Errorf("commit registration: %w", err)
	}
	return NodeCredentials{NodeID: nodeID, AgentID: input.AgentID, Token: rawToken}, nil
}

func (store *Store) MaintainPartitions(ctx context.Context) error {
	if _, err := store.pool.Exec(ctx, `SELECT * FROM monitoring.maintain_partitions(CURRENT_TIMESTAMP)`); err != nil {
		return fmt.Errorf("maintain partitions: %w", err)
	}
	return nil
}

func (store *Store) RollupHour(ctx context.Context, hour time.Time) error {
	if _, err := store.pool.Exec(ctx, `SELECT monitoring.rollup_hour($1)`, hour.UTC().Truncate(time.Hour)); err != nil {
		return fmt.Errorf("roll up hour %s: %w", hour.UTC().Format(time.RFC3339), err)
	}
	return nil
}

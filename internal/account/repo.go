package account

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type Account struct {
	ID          int64
	AccountID   string
	DisplayName string
	Wins        int
	Losses      int
	CreatedAt   time.Time
}

var (
	ErrNotFound      = errors.New("account not found")
	ErrAlreadyExists = errors.New("account_id already taken")
	ErrWrongPassword = errors.New("wrong password")
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) Create(ctx context.Context, accountID, password, displayName string) (*Account, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	var acc Account
	err = r.pool.QueryRow(ctx,
		`INSERT INTO accounts (account_id, password_hash, display_name)
		 VALUES ($1, $2, $3)
		 RETURNING id, account_id, display_name, wins, losses, created_at`,
		accountID, string(hash), displayName,
	).Scan(&acc.ID, &acc.AccountID, &acc.DisplayName, &acc.Wins, &acc.Losses, &acc.CreatedAt)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return &acc, nil
}

func (r *Repo) Authenticate(ctx context.Context, accountID, password string) (*Account, error) {
	var acc Account
	var hash string
	err := r.pool.QueryRow(ctx,
		`SELECT id, account_id, password_hash, display_name, wins, losses, created_at
		 FROM accounts WHERE account_id = $1`,
		accountID,
	).Scan(&acc.ID, &acc.AccountID, &hash, &acc.DisplayName, &acc.Wins, &acc.Losses, &acc.CreatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, ErrWrongPassword
	}
	return &acc, nil
}

func (r *Repo) GetByAccountID(ctx context.Context, accountID string) (*Account, error) {
	var acc Account
	err := r.pool.QueryRow(ctx,
		`SELECT id, account_id, display_name, wins, losses, created_at
		 FROM accounts WHERE account_id = $1`,
		accountID,
	).Scan(&acc.ID, &acc.AccountID, &acc.DisplayName, &acc.Wins, &acc.Losses, &acc.CreatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &acc, err
}

func (r *Repo) AddResult(ctx context.Context, winnerID string, loserIDs []string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE accounts SET wins = wins + 1 WHERE account_id = $1`, winnerID)
	if err != nil {
		return err
	}
	for _, loserID := range loserIDs {
		if _, err := r.pool.Exec(ctx,
			`UPDATE accounts SET losses = losses + 1 WHERE account_id = $1`, loserID); err != nil {
			return err
		}
	}
	return nil
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/libs/libdb"
)

func (s *store) CreateGitHubRepo(ctx context.Context, repo *GitHubRepo) error {
	now := time.Now().UTC()
	repo.CreatedAt = now
	repo.UpdatedAt = now

	_, err := s.Exec.ExecContext(ctx, `
		INSERT INTO github_repos
		(id, user_id, bot_user_name, owner, repo_name, access_token, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		repo.ID,
		repo.UserID,
		repo.BotUserName,
		repo.Owner,
		repo.RepoName,
		repo.AccessToken,
		repo.CreatedAt,
		repo.UpdatedAt,
	)
	return err
}

func (s *store) GetGitHubRepo(ctx context.Context, id string) (*GitHubRepo, error) {
	var repo GitHubRepo
	err := s.Exec.QueryRowContext(ctx, `
		SELECT id, user_id, bot_user_name, owner, repo_name, access_token, created_at, updated_at
		FROM github_repos
		WHERE id = $1`,
		id,
	).Scan(
		&repo.ID,
		&repo.UserID,
		&repo.BotUserName,
		&repo.Owner,
		&repo.RepoName,
		&repo.AccessToken,
		&repo.CreatedAt,
		&repo.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, libdb.ErrNotFound
	}
	return &repo, err
}

func (s *store) DeleteGitHubRepo(ctx context.Context, id string) error {
	result, err := s.Exec.ExecContext(ctx, `
		DELETE FROM github_repos
		WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to delete GitHub repo: %w", err)
	}
	return checkRowsAffected(result)
}

func (s *store) ListGitHubRepos(ctx context.Context) ([]*GitHubRepo, error) {
	rows, err := s.Exec.QueryContext(ctx, `
		SELECT id, user_id, bot_user_name, owner, repo_name, access_token, created_at, updated_at
		FROM github_repos
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query GitHub repos: %w", err)
	}
	defer rows.Close()

	var repos []*GitHubRepo
	for rows.Next() {
		var repo GitHubRepo
		if err := rows.Scan(
			&repo.ID,
			&repo.UserID,
			&repo.BotUserName,
			&repo.Owner,
			&repo.RepoName,
			&repo.AccessToken,
			&repo.CreatedAt,
			&repo.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan GitHub repo: %w", err)
		}
		repos = append(repos, &repo)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return repos, nil
}

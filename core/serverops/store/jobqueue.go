package store

import (
	"context"
	"time"

	_ "github.com/lib/pq"
)

// AppendJobs inserts a list of jobs into the job_queue table.
func (s *store) AppendJob(ctx context.Context, job Job) error {
	job.CreatedAt = time.Now().UTC()
	_, err := s.Exec.ExecContext(ctx, `
		INSERT INTO job_queue_v2
		(id, task_type, operation, subject, entity_id, payload, scheduled_for, valid_until, retry_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);`,
		job.ID,
		job.TaskType,
		job.Operation,
		job.Subject,
		job.EntityID,
		job.Payload,
		job.ScheduledFor,
		job.ValidUntil,
		job.RetryCount,
		job.CreatedAt,
	)

	return err
}

// PopAllJobs removes and returns every job in the job_queue.
func (s *store) PopAllJobs(ctx context.Context) ([]*Job, error) {
	query := `
	DELETE FROM job_queue_v2
	RETURNING id, task_type, operation, subject, entity_id, payload, scheduled_for, valid_until, retry_count, created_at;
	`
	rows, err := s.Exec.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.ID, &job.TaskType, &job.Operation, &job.Subject, &job.EntityID, &job.Payload, &job.ScheduledFor, &job.ValidUntil, &job.RetryCount, &job.CreatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, &job)
	}
	return jobs, nil
}

// PopJobsForType removes and returns all jobs matching a specific task type.
func (s *store) PopJobsForType(ctx context.Context, taskType string) ([]*Job, error) {
	query := `
	DELETE FROM job_queue_v2
	WHERE task_type = $1
	RETURNING id, task_type, operation, subject, entity_id, payload, scheduled_for, valid_until, retry_count, created_at;
	`
	rows, err := s.Exec.QueryContext(ctx, query, taskType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.ID, &job.TaskType, &job.Operation, &job.Subject, &job.EntityID, &job.Payload, &job.ScheduledFor, &job.ValidUntil, &job.RetryCount, &job.CreatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, &job)
	}
	return jobs, nil
}

func (s *store) PopJobForType(ctx context.Context, taskType string) (*Job, error) {
	query := `
	DELETE FROM job_queue_v2
	WHERE id = (
		SELECT id FROM job_queue_v2 WHERE task_type = $1 ORDER BY created_at LIMIT 1
	)
	RETURNING id, task_type, operation, subject, entity_id, payload, scheduled_for, valid_until, retry_count, created_at;
	`
	row := s.Exec.QueryRowContext(ctx, query, taskType)

	var job Job
	if err := row.Scan(&job.ID, &job.TaskType, &job.Operation, &job.Subject, &job.EntityID, &job.Payload, &job.ScheduledFor, &job.ValidUntil, &job.RetryCount, &job.CreatedAt); err != nil {
		return nil, err
	}

	return &job, nil
}

func (s *store) GetJobsForType(ctx context.Context, taskType string) ([]*Job, error) {
	query := `
		SELECT id, task_type, operation, subject, entity_id, payload, scheduled_for, valid_until, retry_count, created_at
		FROM job_queue_v2
		WHERE task_type = $1
		ORDER BY created_at;
	`
	rows, err := s.Exec.QueryContext(ctx, query, taskType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.ID, &job.TaskType, &job.Operation, &job.Subject, &job.EntityID, &job.Payload, &job.ScheduledFor, &job.ValidUntil, &job.RetryCount, &job.CreatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, &job)
	}
	return jobs, nil
}

func (s *store) ListJobs(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*Job, error) {
	query := `
		SELECT id, task_type, operation, subject, entity_id, payload, scheduled_for, valid_until, retry_count, created_at
		FROM job_queue_v2
		WHERE created_at > $1
		ORDER BY created_at
		LIMIT $2;
	`

	rows, err := s.Exec.QueryContext(ctx, query, createdAtCursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.ID, &job.TaskType, &job.Operation, &job.Subject, &job.EntityID, &job.Payload, &job.ScheduledFor, &job.ValidUntil, &job.RetryCount, &job.CreatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, &job)
	}
	return jobs, nil
}

func (s *store) AppendLeasedJob(ctx context.Context, job Job, duration time.Duration, leaser string) error {
	leaseExpiration := time.Now().UTC().Add(duration)
	leaseDurationSeconds := int(duration.Seconds()) // Convert duration to integer seconds

	_, err := s.Exec.ExecContext(ctx, `
		INSERT INTO leased_jobs
		(id, task_type, operation, subject, entity_id, payload, scheduled_for, valid_until, retry_count, created_at, leaser, lease_expiration, lease_duration)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);`,
		job.ID,
		job.TaskType,
		job.Operation,
		job.Subject,
		job.EntityID,
		job.Payload,
		job.ScheduledFor,
		job.ValidUntil,
		job.RetryCount,
		job.CreatedAt,
		leaser,
		leaseExpiration,
		leaseDurationSeconds, // Add duration in seconds as 13th parameter
	)
	return err
}

func (s *store) GetLeasedJob(ctx context.Context, id string) (*LeasedJob, error) {
	query := `
		SELECT id, task_type, operation, subject, entity_id, payload, scheduled_for, valid_until,retry_count, created_at, leaser, lease_expiration
		FROM leased_jobs
		WHERE id = $1;
	`
	row := s.Exec.QueryRowContext(ctx, query, id)

	var job LeasedJob
	if err := row.Scan(&job.ID, &job.TaskType, &job.Operation, &job.Subject, &job.EntityID, &job.Payload, &job.ScheduledFor, &job.ValidUntil, &job.RetryCount, &job.CreatedAt, &job.Leaser, &job.LeaseExpiration); err != nil {
		return nil, err
	}

	return &job, nil
}

func (s *store) DeleteLeasedJob(ctx context.Context, id string) error {
	query := `
		DELETE FROM leased_jobs
		WHERE id = $1;
	`
	_, err := s.Exec.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	return nil
}

func (s *store) ListLeasedJobs(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*LeasedJob, error) {
	query := `
		SELECT id, task_type, operation, subject, entity_id, payload, scheduled_for, valid_until,retry_count, created_at, leaser, lease_expiration
		FROM leased_jobs
		WHERE created_at > $1
		ORDER BY created_at ASC
		LIMIT $2;
	`
	rows, err := s.Exec.QueryContext(ctx, query, createdAtCursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*LeasedJob
	for rows.Next() {
		var job LeasedJob
		if err := rows.Scan(&job.ID, &job.TaskType, &job.Operation, &job.Subject, &job.EntityID, &job.Payload, &job.ScheduledFor, &job.ValidUntil, &job.RetryCount, &job.CreatedAt, &job.Leaser, &job.LeaseExpiration); err != nil {
			return nil, err
		}
		jobs = append(jobs, &job)
	}

	return jobs, nil
}

// Package jobs runs background work on River (a Postgres-backed queue):
// email for publish notifications and workspace invites.
package jobs

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// EmailArgs sends one plain-text email.
type EmailArgs struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func (EmailArgs) Kind() string { return "email" }

// PublishedArgs notifies everyone who opted in that an API version went live.
type PublishedArgs struct {
	WorkspaceID string   `json:"workspace_id"`
	APIName     string   `json:"api_name"`
	Version     string   `json:"version"`
	Changes     []string `json:"changes"`
	DocsURL     string   `json:"docs_url"`
}

func (PublishedArgs) Kind() string { return "api_published" }

type emailWorker struct {
	river.WorkerDefaults[EmailArgs]
	addr, from string
}

func (w *emailWorker) Work(ctx context.Context, job *river.Job[EmailArgs]) error {
	msg := "From: " + w.from + "\r\nTo: " + job.Args.To + "\r\nSubject: " + job.Args.Subject +
		"\r\nContent-Type: text/plain; charset=utf-8\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\n\r\n" + job.Args.Body
	from := w.from
	if i := strings.Index(from, "<"); i >= 0 {
		from = strings.Trim(from[i:], "<>")
	}
	return smtp.SendMail(w.addr, nil, from, []string{job.Args.To}, []byte(msg))
}

type publishedWorker struct {
	river.WorkerDefaults[PublishedArgs]
	pool *pgxpool.Pool
}

func (w *publishedWorker) Work(ctx context.Context, job *river.Job[PublishedArgs]) error {
	// Members whose preference "notifyPublished" isn't explicitly off.
	rows, err := w.pool.Query(ctx, `
		SELECT u.email FROM members m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = $1 AND COALESCE((u.prefs->>'notifyPublished')::bool, true)`, job.Args.WorkspaceID)
	if err != nil {
		return err
	}
	emails, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	client := river.ClientFromContext[pgx.Tx](ctx)
	body := fmt.Sprintf("%s %s is live.\n\nChanges:\n- %s\n\nDocs: %s\n",
		job.Args.APIName, job.Args.Version, strings.Join(job.Args.Changes, "\n- "), job.Args.DocsURL)
	for _, e := range emails {
		if _, err := client.Insert(ctx, EmailArgs{To: e, Subject: job.Args.APIName + " " + job.Args.Version + " published", Body: body}, nil); err != nil {
			return err
		}
	}
	return nil
}

// Start creates and starts the River client.
func Start(ctx context.Context, pool *pgxpool.Pool, smtpAddr, smtpFrom string) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &emailWorker{addr: smtpAddr, from: smtpFrom})
	river.AddWorker(workers, &publishedWorker{pool: pool})
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 5}},
		Workers: workers,
	})
	if err != nil {
		return nil, err
	}
	if err := c.Start(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

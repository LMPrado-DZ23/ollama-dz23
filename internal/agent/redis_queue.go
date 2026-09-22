package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type RedisQueue struct {
	address, password string
	database          int
	prefix            string
	timeout           time.Duration
}

func OpenRedisQueue(ctx context.Context, rawURL, prefix string) (*RedisQueue, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid Redis URL")
	}
	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return nil, errors.New("Redis URL must use redis:// or rediss://")
	}
	database := 0
	if value := strings.TrimPrefix(u.Path, "/"); value != "" {
		database, err = strconv.Atoi(value)
		if err != nil || database < 0 {
			return nil, errors.New("invalid Redis database")
		}
	}
	password, _ := u.User.Password()
	queue := &RedisQueue{address: u.Host, password: password, database: database, prefix: strings.TrimSuffix(prefix, ":"), timeout: 5 * time.Second}
	if queue.prefix == "" {
		queue.prefix = "ollama:agent"
	}
	if err := queue.ping(ctx); err != nil {
		return nil, err
	}
	return queue, nil
}

func (q *RedisQueue) key(name string) string  { return q.prefix + ":" + name }
func (q *RedisQueue) jobKey(id string) string { return q.key("job:" + id) }
func (q *RedisQueue) pendingKey() string      { return q.key("pending") }
func (q *RedisQueue) delayedKey() string      { return q.key("delayed") }
func (q *RedisQueue) deadKey() string         { return q.key("dead") }

func (q *RedisQueue) Enqueue(missionID string, maxAttempts int) (QueueJob, error) {
	if strings.TrimSpace(missionID) == "" {
		return QueueJob{}, errors.New("mission id is required")
	}
	if maxAttempts <= 0 || maxAttempts > 20 {
		maxAttempts = 3
	}
	now := time.Now().UTC()
	job := QueueJob{ID: "job_" + uuid.NewString(), MissionID: missionID, Status: QueuePending, MaxAttempts: maxAttempts, AvailableAt: now, CreatedAt: now, UpdatedAt: now}
	data, _ := json.Marshal(job)
	if _, err := q.do(context.Background(), "SET", q.jobKey(job.ID), string(data), "EX", "604800"); err != nil {
		return QueueJob{}, err
	}
	if _, err := q.do(context.Background(), "LPUSH", q.pendingKey(), job.ID); err != nil {
		return QueueJob{}, err
	}
	return job, nil
}

func (q *RedisQueue) Claim(workerID string, now time.Time) (QueueJob, bool, error) {
	if strings.TrimSpace(workerID) == "" {
		return QueueJob{}, false, errors.New("worker id is required")
	}
	if err := q.moveDue(context.Background(), now); err != nil {
		return QueueJob{}, false, err
	}
	value, err := q.do(context.Background(), "RPOP", q.pendingKey())
	if err != nil {
		return QueueJob{}, false, err
	}
	id, ok := value.(string)
	if !ok || id == "" {
		return QueueJob{}, false, nil
	}
	job, err := q.get(id)
	if err != nil {
		return QueueJob{}, false, err
	}
	if job.Status != QueuePending {
		return QueueJob{}, false, nil
	}
	if job.AvailableAt.After(now) {
		_, _ = q.do(context.Background(), "ZADD", q.delayedKey(), strconv.FormatInt(job.AvailableAt.UnixMilli(), 10), id)
		return QueueJob{}, false, nil
	}
	job.Status = QueueRunning
	job.Attempts++
	job.WorkerID = workerID
	locked := now
	job.LockedAt = &locked
	job.UpdatedAt = now
	if err := q.put(job); err != nil {
		return QueueJob{}, false, err
	}
	return job, true, nil
}

func (q *RedisQueue) Ack(jobID string) error {
	job, err := q.get(jobID)
	if err != nil {
		return err
	}
	job.Status = QueueSucceeded
	job.WorkerID = ""
	job.LockedAt = nil
	job.UpdatedAt = time.Now().UTC()
	return q.put(job)
}
func (q *RedisQueue) Nack(jobID string, runErr error) (QueueJob, error) {
	job, err := q.get(jobID)
	if err != nil {
		return QueueJob{}, err
	}
	if runErr != nil {
		job.LastError = limitError(runErr.Error(), 2000)
	}
	job.WorkerID = ""
	job.LockedAt = nil
	job.UpdatedAt = time.Now().UTC()
	if job.Attempts >= job.MaxAttempts {
		job.Status = QueueDeadLetter
		if err := q.put(job); err != nil {
			return QueueJob{}, err
		}
		_, _ = q.do(context.Background(), "LPUSH", q.deadKey(), job.ID)
		return job, nil
	}
	job.Status = QueuePending
	backoff := time.Duration(1<<(job.Attempts-1)) * time.Second
	if backoff > 5*time.Minute {
		backoff = 5 * time.Minute
	}
	job.AvailableAt = time.Now().UTC().Add(backoff)
	if err := q.put(job); err != nil {
		return QueueJob{}, err
	}
	_, err = q.do(context.Background(), "ZADD", q.delayedKey(), strconv.FormatInt(job.AvailableAt.UnixMilli(), 10), job.ID)
	return job, err
}
func (q *RedisQueue) Replay(jobID string) (QueueJob, error) {
	job, err := q.get(jobID)
	if err != nil {
		return QueueJob{}, err
	}
	if job.Status != QueueDeadLetter && job.Status != QueueFailed {
		return QueueJob{}, fmt.Errorf("job %s is not replayable", jobID)
	}
	job.Status = QueuePending
	job.Attempts = 0
	job.LastError = ""
	job.WorkerID = ""
	job.LockedAt = nil
	job.AvailableAt = time.Now().UTC()
	job.UpdatedAt = time.Now().UTC()
	if err := q.put(job); err != nil {
		return QueueJob{}, err
	}
	_, err = q.do(context.Background(), "LPUSH", q.pendingKey(), job.ID)
	return job, err
}
func (q *RedisQueue) List(status QueueStatus) []QueueJob {
	value, err := q.do(context.Background(), "KEYS", q.key("job:*"))
	if err != nil {
		return nil
	}
	keys, ok := value.([]any)
	if !ok {
		return nil
	}
	jobs := []QueueJob{}
	for _, raw := range keys {
		id, ok := raw.(string)
		if !ok {
			continue
		}
		job, err := q.get(strings.TrimPrefix(id, q.key("job:")))
		if err == nil && (status == "" || job.Status == status) {
			jobs = append(jobs, job)
		}
	}
	return jobs
}
func (q *RedisQueue) Start(ctx context.Context, workerID string, handler func(context.Context, QueueJob) error) {
	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			job, ok, err := q.Claim(workerID, time.Now().UTC())
			if err == nil && ok {
				if runErr := handler(ctx, job); runErr != nil {
					_, _ = q.Nack(job.ID, runErr)
				} else {
					_ = q.Ack(job.ID)
				}
				continue
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()
}

func (q *RedisQueue) ping(ctx context.Context) error { _, err := q.do(ctx, "PING"); return err }
func (q *RedisQueue) get(id string) (QueueJob, error) {
	value, err := q.do(context.Background(), "GET", q.jobKey(id))
	if err != nil {
		return QueueJob{}, err
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return QueueJob{}, osErrNotExist{}
	}
	var job QueueJob
	if err := json.Unmarshal([]byte(text), &job); err != nil {
		return QueueJob{}, err
	}
	return job, nil
}
func (q *RedisQueue) put(job QueueJob) error {
	data, _ := json.Marshal(job)
	_, err := q.do(context.Background(), "SET", q.jobKey(job.ID), string(data), "EX", "604800")
	return err
}
func (q *RedisQueue) moveDue(ctx context.Context, now time.Time) error {
	value, err := q.do(ctx, "ZRANGEBYSCORE", q.delayedKey(), "-inf", strconv.FormatInt(now.UnixMilli(), 10))
	if err != nil {
		return err
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	for _, raw := range items {
		id, ok := raw.(string)
		if !ok {
			continue
		}
		if _, err := q.do(ctx, "ZREM", q.delayedKey(), id); err != nil {
			return err
		}
		if _, err := q.do(ctx, "LPUSH", q.pendingKey(), id); err != nil {
			return err
		}
	}
	return nil
}

func (q *RedisQueue) do(ctx context.Context, args ...string) (any, error) {
	timeout := q.timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", q.address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	reader := bufio.NewReader(conn)
	if q.password != "" {
		if _, err := redisCommand(conn, reader, "AUTH", q.password); err != nil {
			return nil, err
		}
	}
	if q.database > 0 {
		if _, err := redisCommand(conn, reader, "SELECT", strconv.Itoa(q.database)); err != nil {
			return nil, err
		}
	}
	return redisCommand(conn, reader, args...)
}
func redisCommand(w io.Writer, r *bufio.Reader, args ...string) (any, error) {
	var builder strings.Builder
	builder.WriteString("*" + strconv.Itoa(len(args)) + "\r\n")
	for _, arg := range args {
		builder.WriteString("$" + strconv.Itoa(len(arg)) + "\r\n" + arg + "\r\n")
	}
	if _, err := io.WriteString(w, builder.String()); err != nil {
		return nil, err
	}
	return readRedis(r)
}
func readRedis(r *bufio.Reader) (any, error) {
	kind, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	switch kind {
	case '+':
		line, err := r.ReadString('\n')
		return strings.TrimSpace(line), err
	case '-':
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		return nil, errors.New(strings.TrimSpace(line))
	case ':':
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		return strconv.ParseInt(strings.TrimSpace(line), 10, 64)
	case '$':
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			return nil, err
		}
		if size < 0 {
			return nil, nil
		}
		data := make([]byte, size+2)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, err
		}
		return string(data[:size]), nil
	case '*':
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		count, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			return nil, err
		}
		if count < 0 {
			return nil, nil
		}
		items := make([]any, count)
		for index := range items {
			items[index], err = readRedis(r)
			if err != nil {
				return nil, err
			}
		}
		return items, nil
	}
	return nil, fmt.Errorf("unsupported Redis response %q", kind)
}

type osErrNotExist struct{}

func (osErrNotExist) Error() string { return "redis job not found" }

//go:build integration

package agent

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDistributedPostgresRLSAndEvents(t *testing.T) {
	dsn := os.Getenv("OLLAMA_AGENT_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("OLLAMA_AGENT_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := OpenPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	orgA := "org_test_a_" + uuid.NewString()
	orgB := "org_test_b_" + uuid.NewString()
	mission := Mission{ID: "mis_" + uuid.NewString(), Version: 1, Objective: "tenant RLS", OrganizationID: orgA, State: MissionReady, Plan: []Step{}, Approvals: []Approval{}, Artifacts: []ArtifactManifest{}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := store.WithOrganization(orgA).PutMission(mission); err != nil {
		t.Fatal(err)
	}
	if got, err := store.WithOrganization(orgA).GetMission(mission.ID); err != nil || got.OrganizationID != orgA {
		t.Fatalf("same-tenant read failed: got=%+v err=%v", got, err)
	}
	if _, err := store.WithOrganization(orgB).GetMission(mission.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-tenant read should be hidden, got err=%v", err)
	}
	event := Event{ID: "evt_" + uuid.NewString(), MissionID: mission.ID, OrganizationID: orgA, Type: "integration.test", CreatedAt: time.Now().UTC()}
	if err := store.WithOrganization(orgA).AppendEvent(event); err != nil {
		t.Fatal(err)
	}
	if events, err := store.WithOrganization(orgA).ListEvents(mission.ID); err != nil || len(events) != 1 || events[0].OrganizationID != orgA {
		t.Fatalf("same-tenant events failed: events=%+v err=%v", events, err)
	}
	if events, err := store.WithOrganization(orgB).ListEvents(mission.ID); err != nil || len(events) != 0 {
		t.Fatalf("cross-tenant events should be empty: events=%+v err=%v", events, err)
	}
}

func TestDistributedRedisRetriesDeadLetterReplay(t *testing.T) {
	redisURL := os.Getenv("OLLAMA_AGENT_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("OLLAMA_AGENT_TEST_REDIS_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	queue, err := OpenRedisQueue(ctx, redisURL, "ollama:integration:"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	job, err := queue.Enqueue("mis_"+uuid.NewString(), 2)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := queue.Claim("integration-worker", time.Now().UTC())
	if err != nil || !ok || claimed.ID != job.ID {
		t.Fatalf("first claim failed: job=%+v ok=%v err=%v", claimed, ok, err)
	}
	if _, err := queue.Nack(job.ID, errors.New("first failure")); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err = queue.Claim("integration-worker", time.Now().UTC().Add(2*time.Second))
	if err != nil || !ok || claimed.Attempts != 2 {
		t.Fatalf("second claim failed: job=%+v ok=%v err=%v", claimed, ok, err)
	}
	dead, err := queue.Nack(job.ID, errors.New("second failure"))
	if err != nil || dead.Status != QueueDeadLetter {
		t.Fatalf("dead-letter transition failed: job=%+v err=%v", dead, err)
	}
	if _, err := queue.Replay(job.ID); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err = queue.Claim("integration-worker-replay", time.Now().UTC())
	if err != nil || !ok || claimed.Attempts != 1 {
		t.Fatalf("replay claim failed: job=%+v ok=%v err=%v", claimed, ok, err)
	}
	if err := queue.Ack(job.ID); err != nil {
		t.Fatal(err)
	}
	jobs := queue.List(QueueSucceeded)
	found := false
	for _, candidate := range jobs {
		if candidate.ID == job.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("acknowledged job was not listed as succeeded")
	}
}

func TestDistributedOTLPCollector(t *testing.T) {
	endpoint := os.Getenv("OLLAMA_AGENT_TEST_OTLP_ENDPOINT")
	if endpoint == "" {
		t.Skip("OLLAMA_AGENT_TEST_OTLP_ENDPOINT is not configured")
	}
	if len(endpoint) >= 7 && endpoint[:7] == "http://" {
		t.Setenv("OLLAMA_AGENT_OTLP_ALLOW_INSECURE", "1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	telemetry, err := NewTelemetry(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_, span := telemetry.Start(ctx, "integration.collector", map[string]string{"test": "distributed"})
	span.End()
	if err := telemetry.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResearchEngineFetchesSourcesWithCitationsAndCache(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/robots.txt" {
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /blocked\n"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><title>Source</title><body>Evidence about the project.</body></html>"))
	}))
	defer server.Close()
	engine := NewResearchEngine()
	engine.AllowHTTPForTests = true
	engine.MaxConcurrency = 2
	report, err := engine.Research(context.Background(), ResearchRequest{Query: "project", URLs: []string{server.URL + "/one", server.URL + "/two"}, RespectRobots: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Citations) != 2 || !strings.Contains(report.Summary, "Evidence") {
		t.Fatalf("report=%+v", report)
	}
	if _, err := engine.Research(context.Background(), ResearchRequest{Query: "project", URLs: []string{server.URL + "/one"}, RespectRobots: false}); err != nil {
		t.Fatal(err)
	}
	if calls < 3 || calls > 4 {
		t.Fatalf("expected cached source fetches, got %d requests", calls)
	}
}

func TestResearchBlocksPrivateHostsByDefault(t *testing.T) {
	engine := NewResearchEngine()
	report, err := engine.Research(context.Background(), ResearchRequest{Query: "private", URLs: []string{"http://127.0.0.1:1"}})
	if err == nil || len(report.Sources) != 1 || report.Sources[0].Error == "" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

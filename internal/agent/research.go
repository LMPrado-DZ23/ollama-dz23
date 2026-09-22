package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type ResearchRequest struct {
	Query          string   `json:"query"`
	URLs           []string `json:"urls"`
	MaxSources     int      `json:"max_sources,omitempty"`
	MaxBytesSource int64    `json:"max_bytes_source,omitempty"`
	RespectRobots  bool     `json:"respect_robots"`
}

type ResearchSource struct {
	URL         string    `json:"url"`
	Title       string    `json:"title,omitempty"`
	Status      int       `json:"status,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	Text        string    `json:"text,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
	Error       string    `json:"error,omitempty"`
	FetchedAt   time.Time `json:"fetched_at"`
}

type ResearchReport struct {
	Query     string           `json:"query"`
	Sources   []ResearchSource `json:"sources"`
	Summary   string           `json:"summary"`
	Citations []AgentEvidence  `json:"citations"`
	Conflicts []string         `json:"conflicts,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
}

type ResearchEngine struct {
	Client            *http.Client
	MaxConcurrency    int
	MaxBytesSource    int64
	AllowHTTPForTests bool
	mu                sync.Mutex
	cache             map[string]ResearchSource
	robots            map[string][]string
}

func NewResearchEngine() *ResearchEngine {
	return &ResearchEngine{Client: &http.Client{Timeout: 30 * time.Second}, MaxConcurrency: 4, MaxBytesSource: 2 << 20, cache: map[string]ResearchSource{}, robots: map[string][]string{}}
}

func (e *ResearchEngine) Research(ctx context.Context, request ResearchRequest) (ResearchReport, error) {
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return ResearchReport{}, errors.New("research query is required")
	}
	if len(request.URLs) == 0 {
		return ResearchReport{}, errors.New("research requires at least one URL")
	}
	if request.MaxSources <= 0 || request.MaxSources > 32 {
		request.MaxSources = 16
	}
	if len(request.URLs) > request.MaxSources {
		request.URLs = request.URLs[:request.MaxSources]
	}
	if request.MaxBytesSource <= 0 || request.MaxBytesSource > 10<<20 {
		request.MaxBytesSource = e.MaxBytesSource
	}
	if request.MaxBytesSource <= 0 {
		request.MaxBytesSource = 2 << 20
	}
	workers := e.MaxConcurrency
	if workers <= 0 || workers > 8 {
		workers = 4
	}
	semaphore := make(chan struct{}, workers)
	results := make([]ResearchSource, len(request.URLs))
	var wait sync.WaitGroup
	for index, rawURL := range request.URLs {
		wait.Add(1)
		go func(index int, rawURL string) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				results[index] = ResearchSource{URL: rawURL, Error: ctx.Err().Error(), FetchedAt: time.Now().UTC()}
				return
			}
			defer func() { <-semaphore }()
			results[index] = e.fetch(ctx, strings.TrimSpace(rawURL), request.MaxBytesSource, request.RespectRobots)
		}(index, rawURL)
	}
	wait.Wait()
	if err := ctx.Err(); err != nil {
		return ResearchReport{}, err
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].URL < results[j].URL })
	report := ResearchReport{Query: request.Query, Sources: results, CreatedAt: time.Now().UTC()}
	var summary strings.Builder
	for index, source := range results {
		if source.Error != "" || source.Text == "" {
			continue
		}
		summary.WriteString(fmt.Sprintf("[%d] %s\n%s\n\n", index+1, source.Title, truncateResearch(source.Text, 4000)))
		report.Citations = append(report.Citations, AgentEvidence{URL: source.URL, Title: source.Title, Excerpt: truncateResearch(source.Text, 500), SHA256: source.SHA256, Source: "research"})
	}
	report.Summary = summary.String()
	if report.Summary == "" {
		return report, errors.New("no research source produced readable content")
	}
	return report, nil
}

func (e *ResearchEngine) fetch(ctx context.Context, rawURL string, maxBytes int64, respectRobots bool) ResearchSource {
	result := ResearchSource{URL: rawURL, FetchedAt: time.Now().UTC()}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(e.AllowHTTPForTests && parsed.Scheme == "http")) {
		result.Error = "only public HTTPS URLs are allowed"
		return result
	}
	if !(e.AllowHTTPForTests && parsed.Scheme == "http") {
		if err := publicHost(parsed.Hostname()); err != nil {
			result.Error = err.Error()
			return result
		}
	}
	if respectRobots && !e.allowedByRobots(ctx, parsed) {
		result.Error = "blocked by robots policy"
		return result
	}
	e.mu.Lock()
	if cached, ok := e.cache[rawURL]; ok {
		e.mu.Unlock()
		return cached
	}
	e.mu.Unlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	request.Header.Set("User-Agent", "ollama-dz23-research/1")
	response, err := e.client().Do(request)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer response.Body.Close()
	result.Status = response.StatusCode
	result.ContentType = response.Header.Get("Content-Type")
	if response.StatusCode/100 != 2 {
		result.Error = fmt.Sprintf("HTTP status %d", response.StatusCode)
		return result
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	sum := sha256.Sum256(body)
	result.SHA256 = hex.EncodeToString(sum[:])
	result.Title, result.Text = extractResearchText(string(body), result.ContentType)
	if result.Text == "" {
		result.Error = "source has no readable text"
		return result
	}
	e.mu.Lock()
	e.cache[rawURL] = result
	e.mu.Unlock()
	return result
}

func (e *ResearchEngine) allowedByRobots(ctx context.Context, target *url.URL) bool {
	key := target.Scheme + "://" + target.Host
	e.mu.Lock()
	rules, cached := e.robots[key]
	e.mu.Unlock()
	if !cached {
		robotsURL := key + "/robots.txt"
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
		if err != nil {
			return false
		}
		response, err := e.client().Do(request)
		if err != nil {
			return true
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 256<<10))
		_ = response.Body.Close()
		rules = parseRobots(string(body))
		e.mu.Lock()
		e.robots[key] = rules
		e.mu.Unlock()
	}
	path := target.EscapedPath()
	for _, rule := range rules {
		if rule == "/" || strings.HasPrefix(path, rule) {
			return false
		}
	}
	return true
}
func parseRobots(body string) []string {
	var rules []string
	active := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(parts[1])
		if key == "user-agent" {
			active = value == "*"
		}
		if active && key == "disallow" && value != "" {
			rules = append(rules, value)
		}
	}
	return rules
}
func publicHost(host string) error {
	if net.ParseIP(host) != nil {
		ip := net.ParseIP(host)
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return errors.New("private or local research host is blocked")
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return errors.New("research host resolves to a private or local address")
		}
	}
	return nil
}
func (e *ResearchEngine) client() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return http.DefaultClient
}
func truncateResearch(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

var tagRE = regexp.MustCompile(`(?s)<[^>]*>`)
var invisibleTagRE = regexp.MustCompile(`(?is)<(?:script|style|noscript)[^>]*>.*?</(?:script|style|noscript)>`)
var spaceRE = regexp.MustCompile(`\s+`)

func extractResearchText(body, contentType string) (string, string) {
	title := ""
	text := body
	if strings.Contains(strings.ToLower(contentType), "html") || strings.Contains(strings.ToLower(body[:minResearchInt(len(body), 200)]), "<html") {
		lower := strings.ToLower(body)
		start, end := strings.Index(lower, "<title>"), strings.Index(lower, "</title>")
		if start >= 0 && end > start {
			title = html.UnescapeString(strings.TrimSpace(body[start+7 : end]))
		}
		text = invisibleTagRE.ReplaceAllString(text, "")
		text = tagRE.ReplaceAllString(text, " ")
	}
	text = html.UnescapeString(text)
	text = spaceRE.ReplaceAllString(text, " ")
	return title, strings.TrimSpace(text)
}
func minResearchInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

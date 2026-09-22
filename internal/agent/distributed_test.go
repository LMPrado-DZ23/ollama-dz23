package agent

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

func TestTelemetryNoopStartsSpan(t *testing.T) {
	telemetry, err := NewTelemetry(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := telemetry.Start(context.Background(), "test", map[string]string{"component": "agent"})
	if ctx == nil || span == nil {
		t.Fatal("noop telemetry did not return span")
	}
	span.End()
}

func TestRedisRESPParser(t *testing.T) {
	value, err := readRedis(bufio.NewReader(strings.NewReader("*2\r\n$3\r\nfoo\r\n:7\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	items, ok := value.([]any)
	if !ok || len(items) != 2 || items[0] != "foo" || items[1].(int64) != 7 {
		t.Fatalf("value=%#v", value)
	}
}

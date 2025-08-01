package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/prompb"
)

func TestCreateTimeSeries(t *testing.T) {
	ts := createTimeSeries("test_metric", 123.45, 1690000000000, "http://server", "host1")

	if len(ts.Labels) != 3 {
		t.Errorf("Expected 3 labels, got %d", len(ts.Labels))
	}
	if ts.Samples[0].Value != 123.45 {
		t.Errorf("Expected value 123.45, got %f", ts.Samples[0].Value)
	}
}

func TestGetLabelValue(t *testing.T) {
	labels := []prompb.Label{
		{Name: "__name__", Value: "metric"},
		{Name: "instance", Value: "host1"},
	}
	val := getLabelValue(labels, "instance")
	if val != "host1" {
		t.Errorf("Expected 'host1', got '%s'", val)
	}
}

func TestValidateLogFilePath_Valid(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	err := validateLogFilePath(logPath)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

func TestValidateLogFilePath_Invalid(t *testing.T) {
	invalidPath := "/nonexistent/path/test.log"
	err := validateLogFilePath(invalidPath)
	if err == nil {
		t.Error("Expected error for nonexistent directory, got nil")
	}
}

func TestSendToRemoteWrite_Success(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	ts := createTimeSeries("test_metric", 1.0, time.Now().UnixMilli(), "server", "instance")
	err := sendToRemoteWrite(mockServer.URL, "user", "pass", []*prompb.TimeSeries{ts})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

func TestSendToRemoteWrite_Non200Response(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	}))
	defer mockServer.Close()

	ts := createTimeSeries("test_metric", 1.0, time.Now().UnixMilli(), "server", "instance")
	err := sendToRemoteWrite(mockServer.URL, "user", "pass", []*prompb.TimeSeries{ts})
	if err == nil {
		t.Error("Expected error for non-200 response, got nil")
	}
}

func TestSendToRemoteWrite_InvalidURL(t *testing.T) {
	ts := createTimeSeries("test_metric", 1.0, time.Now().UnixMilli(), "server", "instance")
	err := sendToRemoteWrite(":", "user", "pass", []*prompb.TimeSeries{ts})
	if err == nil {
		t.Error("Expected error for invalid URL, got nil")
	}
}

func TestRunLibrespeed_Success(t *testing.T) {
	mockOutput := `[{"download":100.5,"upload":50.2,"ping":10.1,"jitter":1.2,"server":{"url":"http://test"}}]`
	runner := &MockRunner{Output: []byte(mockOutput)}

	result, err := runLibrespeed(runner, "")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if result.Download != 100.5 {
		t.Errorf("Expected download 100.5, got %f", result.Download)
	}
}

func TestRunLibrespeed_WithLocalJSON(t *testing.T) {
	mockOutput := `[{"download":200.0,"upload":100.0,"ping":5.0,"jitter":0.5,"server":{"url":"http://custom"}}]`
	runner := &MockRunner{Output: []byte(mockOutput)}

	result, err := runLibrespeed(runner, "/path/to/servers.json")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if result.Server.URL != "http://custom" {
		t.Errorf("Expected server URL 'http://custom', got '%s'", result.Server.URL)
	}
	if !strings.Contains(runner.LastArgs(), "--local-json") {
		t.Error("Expected '--local-json' argument to be passed")
	}
}

func TestRunLibrespeed_InvalidJSON(t *testing.T) {
	runner := &MockRunner{Output: []byte("invalid json")}
	_, err := runLibrespeed(runner, "")
	if err == nil {
		t.Error("Expected JSON parse error, got nil")
	}
}

func TestRunLibrespeed_CommandError(t *testing.T) {
	runner := &MockRunner{Err: fmt.Errorf("command failed")}
	_, err := runLibrespeed(runner, "")
	if err == nil {
		t.Error("Expected command error, got nil")
	}
}

// MockRunner implements CommandRunner for testing
type MockRunner struct {
	Output   []byte
	Err      error
	lastArgs []string
}

func (m *MockRunner) Run(name string, args ...string) ([]byte, error) {
	m.lastArgs = args
	return m.Output, m.Err
}

func (m *MockRunner) LastArgs() string {
	return strings.Join(m.lastArgs, " ")
}

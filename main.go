package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
)

type CommandRunner interface {
	Run(name string, args ...string) ([]byte, error)
}

type DefaultRunner struct{}

func (r *DefaultRunner) Run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		log.Printf("librespeed-cli error output: %s", stderr.String())
		return nil, fmt.Errorf("command failed: %v", err)
	}
	return out.Bytes(), nil
}

type ServerInfo struct {
	ID  int    `json:"id"`
	URL string `json:"url"`
}

type LibrespeedResult struct {
	Download float64    `json:"download"`
	Upload   float64    `json:"upload"`
	Ping     float64    `json:"ping"`
	Jitter   float64    `json:"jitter"`
	Server   ServerInfo `json:"server"`
}

func ensureLibrespeedCLI() (string, error) {
	log.Println("Checking for librespeed-cli...")
	
	exePath, err := exec.LookPath("librespeed-cli.exe")
	if err == nil {
		log.Printf("Found librespeed-cli at: %s", exePath)
		return exePath, nil
	}

	installDir := `C:\librespeed-cli`
	exePath = filepath.Join(installDir, "librespeed-cli.exe")

	if _, err := os.Stat(exePath); err == nil {
		log.Printf("Found librespeed-cli in install directory: %s", installDir)
		os.Setenv("PATH", installDir+";"+os.Getenv("PATH"))
		return exePath, nil
	}

	log.Println("librespeed-cli not found. Downloading...")

	err = os.MkdirAll(installDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create install directory: %v", err)
	}

	zipURL := "https://github.com/librespeed/speedtest-cli/releases/download/v1.0.12/librespeed-cli_1.0.12_windows_amd64.zip"
	
	// Create HTTP client with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "GET", zipURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %v", err)
	}
	
	log.Printf("Downloading from: %s", zipURL)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download ZIP: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status: %s", resp.Status)
	}

	log.Printf("Download successful, status: %s", resp.Status)

	zipPath := filepath.Join(installDir, "librespeed-cli.zip")
	out, err := os.Create(zipPath)
	if err != nil {
		return "", fmt.Errorf("failed to create ZIP file: %v", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to save ZIP file: %v", err)
	}

	log.Println("Extracting librespeed-cli...")

	// Extract the ZIP
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("failed to open ZIP: %v", err)
	}
	defer r.Close()

	found := false
	for _, f := range r.File {
		if strings.EqualFold(f.Name, "librespeed-cli.exe") {
			rc, err := f.Open()
			if err != nil {
				return "", fmt.Errorf("failed to open file in ZIP: %v", err)
			}
			defer rc.Close()

			outExe, err := os.Create(exePath)
			if err != nil {
				return "", fmt.Errorf("failed to create EXE file: %v", err)
			}
			defer outExe.Close()

			_, err = io.Copy(outExe, rc)
			if err != nil {
				return "", fmt.Errorf("failed to extract EXE: %v", err)
			}
			found = true
			break
		}
	}

	if !found {
		return "", fmt.Errorf("librespeed-cli.exe not found in downloaded ZIP file")
	}

	log.Printf("Successfully installed librespeed-cli to: %s", exePath)
	os.Setenv("PATH", installDir+";"+os.Getenv("PATH"))
	return exePath, nil
}

func runLibrespeed(runner CommandRunner, cliPath, localJSONPath string, serverID *int) (*LibrespeedResult, error) {
	log.Println("Running librespeed-cli...")
	start := time.Now()

	args := []string{"--telemetry-level", "basic", "--json", "--verbose"}

	if serverID != nil && localJSONPath != "" {
		args = append(args, "--local-json", localJSONPath, "--server", fmt.Sprintf("%d", *serverID))
	} else if localJSONPath != "" {
		args = append(args, "--local-json", localJSONPath)
	}
	
	log.Printf("Running command: %s %s", cliPath, strings.Join(args, " "))
	output, err := runner.Run(cliPath, args...)
	duration := time.Since(start)
	
	if err != nil {
		log.Printf("librespeed-cli failed after %v: %v", duration, err)
		return nil, fmt.Errorf("failed to run librespeed-cli: %v", err)
	}
	
	log.Printf("librespeed-cli completed in %v", duration)
	log.Printf("librespeed-cli raw output: %s", string(output))

	var results []LibrespeedResult
	if err := json.Unmarshal(output, &results); err != nil {
		log.Printf("Failed to parse JSON output: %v", err)
		return nil, fmt.Errorf("failed to parse JSON: %v", err)
	}
	if len(results) == 0 {
		log.Println("No results returned from librespeed-cli")
		return nil, fmt.Errorf("no results returned from librespeed-cli")
	}
	
	result := &results[0]
	log.Printf("Speed test results - Download: %.2f Mbps, Upload: %.2f Mbps, Ping: %.2f ms, Jitter: %.2f ms", 
		result.Download, result.Upload, result.Ping, result.Jitter)
		
	return result, nil
}

func createTimeSeries(metric string, value float64, ts int64, serverURL, instance string) *prompb.TimeSeries {
	return &prompb.TimeSeries{
		Labels: []prompb.Label{
			{Name: "__name__", Value: metric},
			{Name: "server_url", Value: serverURL},
			{Name: "instance", Value: instance},
		},
		Samples: []prompb.Sample{
			{Value: value, Timestamp: ts},
		},
	}
}

func getLabelValue(labels []prompb.Label, name string) string {
	for _, label := range labels {
		if label.Name == name {
			return label.Value
		}
	}
	return ""
}

func sendToRemoteWrite(url, username, password string, series []*prompb.TimeSeries) error {
	if len(series) == 0 {
		return fmt.Errorf("no time series data to send")
	}
	
	log.Printf("Preparing to send %d metrics to remote write endpoint", len(series))
	
	var tsList []prompb.TimeSeries
	for _, ts := range series {
		log.Printf("Sending metric: %s | Server: %s | Instance: %s | Value: %.2f | Timestamp: %d",
			getLabelValue(ts.Labels, "__name__"),
			getLabelValue(ts.Labels, "server_url"),
			getLabelValue(ts.Labels, "instance"),
			ts.Samples[0].Value,
			ts.Samples[0].Timestamp,
		)
		tsList = append(tsList, *ts)
	}

	req := &prompb.WriteRequest{
		Timeseries: tsList,
	}

	data, err := req.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf: %v", err)
	}

	compressed := snappy.Encode(nil, data)
	log.Printf("Payload size: %d bytes (compressed: %d bytes)", len(data), len(compressed))

	reqBody := bytes.NewReader(compressed)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %v", err)
	}

	httpReq.Header.Set("Content-Encoding", "snappy")
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	httpReq.SetBasicAuth(username, password)

	client := &http.Client{Timeout: 30 * time.Second}
	start := time.Now()
	resp, err := client.Do(httpReq)
	duration := time.Since(start)
	
	if err != nil {
		log.Printf("HTTP request failed after %v: %v", duration, err)
		return fmt.Errorf("failed to send HTTP request: %v", err)
	}
	defer resp.Body.Close()

	log.Printf("Received response: %s (duration: %v)", resp.Status, duration)

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Remote write failed with response body: %s", string(body))
		return fmt.Errorf("remote_write failed: %s - %s", resp.Status, string(body))
	}

	log.Println("Metrics sent successfully to remote write endpoint")
	return nil
}

// For testing, we can use a shorter delay
var retryDelayFunc = func(attempt int) time.Duration {
	backoffSeconds := (1 << (attempt - 1)) + rand.Intn(1<<(attempt-1))
	if backoffSeconds > 30 {
		backoffSeconds = 30
	}
	return time.Duration(backoffSeconds) * time.Second
}

func sendToRemoteWriteWithRetry(url, username, password string, series []*prompb.TimeSeries, maxRetries int) error {
	var lastErr error
	
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := retryDelayFunc(attempt)
			log.Printf("Retrying in %v (attempt %d/%d)", delay, attempt+1, maxRetries+1)
			time.Sleep(delay)
		}
		
		err := sendToRemoteWrite(url, username, password, series)
		if err == nil {
			if attempt > 0 {
				log.Printf("Successfully sent metrics after %d retries", attempt)
			}
			return nil
		}
		
		lastErr = err
		log.Printf("Attempt %d failed: %v", attempt+1, err)
		
		// Don't retry on certain types of errors (authentication, bad request, etc.)
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "403") || 
		   strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "404") {
			log.Printf("Non-retryable error detected, stopping retries: %v", err)
			break
		}
	}
	
	return fmt.Errorf("failed after %d attempts, last error: %v", maxRetries+1, lastErr)
}

func validateLogFilePath(path string) error {
	if path == "" {
		return fmt.Errorf("log file path cannot be empty")
	}
	
	dir := filepath.Dir(path)
	if stat, err := os.Stat(dir); os.IsNotExist(err) || !stat.IsDir() {
		return fmt.Errorf("log file directory does not exist: %s", dir)
	}
	return nil
}

func validateConfiguration(remoteWriteURL, username, password string) error {
	if remoteWriteURL == "" {
		return fmt.Errorf("remote write URL is required")
	}
	if username == "" {
		return fmt.Errorf("username is required")
	}
	if password == "" {
		return fmt.Errorf("password is required")
	}
	
	// Validate URL format
	parsedURL, err := url.Parse(remoteWriteURL)
	if err != nil {
		return fmt.Errorf("invalid remote write URL format: %v", err)
	}
	
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("remote write URL must use http or https scheme")
	}
	
	if parsedURL.Host == "" {
		return fmt.Errorf("remote write URL must include a host")
	}
	
	log.Printf("Configuration validated - URL: %s, Username: %s", remoteWriteURL, username)
	return nil
}

func main() {
	logFilePath := flag.String("logfile", "librespeed_exporter.log", "Path to the log file")
	url := flag.String("url", "", "Grafana Cloud remote_write URL")
	username := flag.String("username", "", "Grafana Cloud instance ID")
	password := flag.String("password", "", "Grafana Cloud API key")
	localJSONPath := flag.String("local-json", "", "Path to JSON file with server list")
	serverID := flag.Int("server-id", 1, "ID of the server to use from the JSON list")
	flag.Parse()

	log.Println("Starting librespeed exporter...")
	log.Printf("Version: librespeed-go (production-ready)")
	log.Printf("Log file: %s", *logFilePath)

	if err := validateLogFilePath(*logFilePath); err != nil {
		log.Printf("Invalid log file path: %v", err)
		fmt.Fprintf(os.Stderr, "Invalid log file path: %v\n", err)
		os.Exit(1)
	}

	logFile, err := os.OpenFile(*logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Printf("Failed to open log file: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to open log file: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := logFile.Close(); closeErr != nil {
			log.Printf("Error closing log file: %v", closeErr)
		}
	}()

	log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// Validate required parameters and configuration
	if err := validateConfiguration(*url, *username, *password); err != nil {
		log.Printf("ERROR: Configuration validation failed: %v", err)
		fmt.Fprintf(os.Stderr, "ERROR: Configuration validation failed: %v\n", err)
		os.Exit(1)
	}

	start := time.Now()
	
	cliPath, err := ensureLibrespeedCLI()
	if err != nil {
		log.Printf("ERROR: Failed to ensure librespeed-cli: %v", err)
		os.Exit(1)
	}

	result, err := runLibrespeed(&DefaultRunner{}, cliPath, *localJSONPath, serverID)
	if err != nil {
		log.Printf("ERROR: Failed to run librespeed test: %v", err)
		os.Exit(1)
	}

	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("WARNING: Failed to get hostname, using 'unknown': %v", err)
		hostname = "unknown"
	}
	
	log.Printf("Instance hostname: %s", hostname)

	now := time.Now().UnixMilli()
	series := []*prompb.TimeSeries{
		createTimeSeries("librespeed_download_mbps", result.Download, now, result.Server.URL, hostname),
		createTimeSeries("librespeed_upload_mbps", result.Upload, now, result.Server.URL, hostname),
		createTimeSeries("librespeed_ping_ms", result.Ping, now, result.Server.URL, hostname),
		createTimeSeries("librespeed_jitter_ms", result.Jitter, now, result.Server.URL, hostname),
	}

	if err := sendToRemoteWriteWithRetry(*url, *username, *password, series, 3); err != nil {
		log.Printf("ERROR: Failed to send metrics after retries: %v", err)
		os.Exit(1)
	}

	totalDuration := time.Since(start)
	log.Printf("SUCCESS: Librespeed exporter completed successfully in %v", totalDuration)
}

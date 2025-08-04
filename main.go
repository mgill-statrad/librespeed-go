package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
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
	exePath, err := exec.LookPath("librespeed-cli.exe")
	if err == nil {
		return exePath, nil
	}

	installDir := `C:\librespeed-cli`
	exePath = filepath.Join(installDir, "librespeed-cli.exe")

	if _, err := os.Stat(exePath); err == nil {
		os.Setenv("PATH", installDir+";"+os.Getenv("PATH"))
		return exePath, nil
	}

	fmt.Println("librespeed-cli not found. Downloading...")

	err = os.MkdirAll(installDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create install directory: %v", err)
	}

	zipURL := "https://github.com/librespeed/speedtest-cli/releases/download/v1.0.12/librespeed-cli_1.0.12_windows_amd64.zip"
	resp, err := http.Get(zipURL)
	if err != nil {
		return "", fmt.Errorf("failed to download ZIP: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status: %s", resp.Status)
	}

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

	// Extract the ZIP
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("failed to open ZIP: %v", err)
	}
	defer r.Close()

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
			break
		}
	}

	os.Setenv("PATH", installDir+";"+os.Getenv("PATH"))
	return exePath, nil
}

func runLibrespeed(runner CommandRunner, cliPath, localJSONPath string, serverID *int) (*LibrespeedResult, error) {
	log.Println("Running librespeed-cli...")

	args := []string{"--telemetry-level", "basic", "--json", "--verbose"}

	if serverID != nil && localJSONPath != "" {
		args = append(args, "--local-json", localJSONPath, "--server", fmt.Sprintf("%d", *serverID))
	} else if localJSONPath != "" {
		args = append(args, "--local-json", localJSONPath)
	}
	log.Printf("Running command: %s %s", cliPath, args)
	output, err := runner.Run(cliPath, args...)
	log.Printf("librespeed-cli raw output: %s", string(output))

	if err != nil {
		return nil, fmt.Errorf("failed to run librespeed-cli: %v", err)
	}

	var results []LibrespeedResult
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %v", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no results returned from librespeed-cli")
	}
	return &results[0], nil
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

	reqBody := bytes.NewReader(compressed)
	httpReq, err := http.NewRequest("POST", url, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %v", err)
	}

	httpReq.Header.Set("Content-Encoding", "snappy")
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	httpReq.SetBasicAuth(username, password)

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send HTTP request: %v", err)
	}
	defer resp.Body.Close()

	log.Printf("Received response: %s", resp.Status)

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remote_write failed: %s - %s", resp.Status, string(body))
	}

	return nil
}

func validateLogFilePath(path string) error {
	dir := filepath.Dir(path)
	if stat, err := os.Stat(dir); os.IsNotExist(err) || !stat.IsDir() {
		return fmt.Errorf("log file directory does not exist: %s", dir)
	}
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

	if err := validateLogFilePath(*logFilePath); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid log file path: %v", err)
		os.Exit(1)
	}

	logFile, err := os.OpenFile(*logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open log file: %v", err)
		os.Exit(1)
	}
	defer logFile.Close()

	log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	log.SetFlags(log.LstdFlags)

	log.Println("Starting librespeed exporter...")

	if *url == "" || *username == "" || *password == "" {
		log.Println("All flags --url, --username, and --password are required.")
		os.Exit(1)
	}

	cliPath, err := ensureLibrespeedCLI()
	if err != nil {
		log.Printf("Error ensuring librespeed-cli: %v", err)
		os.Exit(1)
	}

	result, err := runLibrespeed(&DefaultRunner{}, cliPath, *localJSONPath, serverID)
	if err != nil {
		log.Printf("Error running librespeed: %v", err)
		os.Exit(1)
	}

	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Error getting hostname: %v", err)
		os.Exit(1)
	}

	now := time.Now().UnixMilli()
	series := []*prompb.TimeSeries{
		createTimeSeries("librespeed_download_mbps", result.Download, now, result.Server.URL, hostname),
		createTimeSeries("librespeed_upload_mbps", result.Upload, now, result.Server.URL, hostname),
		createTimeSeries("librespeed_ping_ms", result.Ping, now, result.Server.URL, hostname),
		createTimeSeries("librespeed_jitter_ms", result.Jitter, now, result.Server.URL, hostname),
	}

	if err := sendToRemoteWrite(*url, *username, *password, series); err != nil {
		log.Printf("Error sending metrics: %v", err)
		os.Exit(1)
	}

	log.Println("Metrics sent successfully.")
}

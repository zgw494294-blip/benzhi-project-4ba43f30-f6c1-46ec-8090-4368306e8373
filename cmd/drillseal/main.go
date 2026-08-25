package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/httpapi"
	"drill-seal-handover/internal/service"
	"drill-seal-handover/internal/store"
)

const defaultListenAddress = "127.0.0.1:19081"

type config struct {
	address   string
	database  string
	selfcheck bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("启动失败: %v", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	defaults, err := configuredAddress(os.Getenv("PORT"))
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("drillseal", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cfg := config{}
	flags.StringVar(&cfg.address, "addr", defaults, "HTTP 监听地址")
	flags.StringVar(&cfg.database, "db", "data/drillseal.db", "SQLite 数据库路径")
	flags.BoolVar(&cfg.selfcheck, "selfcheck", false, "运行有界自检并退出")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("解析参数: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("存在未识别参数: %s", strings.Join(flags.Args(), " "))
	}
	if err := validateAddress(cfg.address); err != nil {
		return err
	}
	if cfg.selfcheck {
		return runSelfcheck(cfg.address)
	}
	if err := prepareDatabaseDirectory(cfg.database); err != nil {
		return fmt.Errorf("创建数据目录: %w", err)
	}
	repository, err := store.Open(cfg.database)
	if err != nil {
		return fmt.Errorf("打开数据库: %w", err)
	}
	defer repository.Close()
	app := service.New(repository)
	server := &http.Server{Addr: cfg.address, Handler: httpapi.New(app).Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	listener, err := net.Listen("tcp", cfg.address)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", cfg.address, err)
	}
	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan error, 1)
	go func() {
		log.Printf("钻孔封孔移交工作台已启动: http://%s/workbench", cfg.address)
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-shutdownSignal:
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		return server.Shutdown(ctx)
	}
}

func addressFromEnvironment(port string) (string, error) {
	port = strings.TrimSpace(port)
	if port == "" {
		return defaultListenAddress, nil
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1024 || number > 65535 {
		return "", fmt.Errorf("PORT 必须是 1024 到 65535 的端口号")
	}
	return net.JoinHostPort("127.0.0.1", port), nil
}
func validateAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("监听地址必须为 host:port: %w", err)
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return fmt.Errorf("监听地址必须使用回环主机，不能使用 %q", host)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1024 || number > 65535 {
		return fmt.Errorf("监听端口必须在 1024 到 65535 之间")
	}
	return nil
}

func runSelfcheck(address string) error {
	repository, err := store.Open("file:drillseal-selfcheck?mode=memory&cache=shared")
	if err != nil {
		return fmt.Errorf("自检数据库: %w", err)
	}
	defer repository.Close()
	server := &http.Server{Handler: httpapi.New(service.New(repository)).Handler(), ReadHeaderTimeout: 2 * time.Second}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("自检监听 %s: %w", address, err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	baseURL := "http://" + listener.Addr().String()
	client := selfcheckHTTPClient()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	defer func() {
		shutdownCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = server.Shutdown(shutdownCtx)
		<-done
	}()
	manager := domain.Actor{Name: "自检负责人", Role: domain.RoleManager}
	reviewer := domain.Actor{Name: "自检复核员", Role: domain.RoleReviewer}
	worker := domain.Actor{Name: "自检施工员", Role: domain.RoleWorker}
	var task domain.SealTask
	if err := postJSON(ctx, client, baseURL+"/api/v1/tasks", map[string]any{"taskCode": "SELF-CHECK", "siteName": "自检场地", "boreholeNo": "ZK-S01", "collarEasting": 100.2, "collarNorthing": 200.4, "totalDepthM": 20, "strataSummary": "0-20 米稳定岩层", "actor": manager}, &task); err != nil {
		return err
	}
	var aggregate domain.Aggregate
	if err := postJSON(ctx, client, baseURL+"/api/v1/tasks/"+task.ID+"/plan", map[string]any{"expectedVersion": task.Version, "actor": manager, "segments": []map[string]any{{"sequence": 1, "fromDepthM": 0, "toDepthM": 20, "materialType": "水泥浆", "plannedVolumeL": 120, "mixRatio": "1:1"}}}, &aggregate); err != nil {
		return err
	}
	segment := aggregate.Segments[0]
	var construction any
	if err := postJSON(ctx, client, baseURL+"/api/v1/tasks/"+task.ID+"/segments", map[string]any{"segmentId": segment.ID, "expectedVersion": aggregate.Task.Version, "idempotencyKey": "self-construction", "actualVolumeL": 120, "actualMixRatio": "1:1", "materialBatch": "SC-001", "performedAt": time.Now().UTC(), "operator": "自检施工员", "result": "complete", "actor": worker}, &construction); err != nil {
		return err
	}
	if err := getJSON(ctx, client, baseURL+"/api/v1/tasks/"+task.ID, &aggregate); err != nil {
		return err
	}
	if err := postJSON(ctx, client, baseURL+"/api/v1/tasks/"+task.ID+"/freeze", map[string]any{"expectedVersion": aggregate.Task.Version, "idempotencyKey": "self-freeze", "actor": reviewer}, &task); err != nil {
		return err
	}
	var credential domain.HandoverCredential
	if err := postJSON(ctx, client, baseURL+"/api/v1/tasks/"+task.ID+"/credential", map[string]any{"expectedVersion": task.Version, "idempotencyKey": "self-credential", "actor": manager}, &credential); err != nil {
		return err
	}
	var verified struct {
		Valid bool `json:"valid"`
	}
	if err := getJSON(ctx, client, baseURL+"/api/v1/tasks/"+task.ID+"/verify", &verified); err != nil {
		return err
	}
	if !verified.Valid || credential.SerialNo == "" {
		return fmt.Errorf("自检凭据校验未通过")
	}
	log.Printf("selfcheck 通过: task=%s credential=%s digest=%s", task.ID, credential.SerialNo, credential.ManifestDigest)
	return nil
}

func postJSON(ctx context.Context, client *http.Client, url string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	return executeJSON(client, request, output)
}
func getJSON(ctx context.Context, client *http.Client, url string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	return executeJSON(client, request, output)
}
func executeJSON(client *http.Client, request *http.Request, output any) error {
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("自检请求 %s: %w", request.URL.Path, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("自检请求 %s 返回 %d: %s", request.URL.Path, response.StatusCode, body)
	}
	if output != nil && len(body) > 0 {
		if err := json.Unmarshal(body, output); err != nil {
			return fmt.Errorf("解析自检响应: %w", err)
		}
	}
	return nil
}

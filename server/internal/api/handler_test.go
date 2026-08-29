package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tokendance/token-collector/server/internal/auth"
	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/ingest"
	"github.com/tokendance/token-collector/server/internal/protocol"
	"github.com/tokendance/token-collector/server/internal/store/memstore"
)

func TestLeaderboardResponseIncludesDataWatermarkAndLag(t *testing.T) {
	ctx := context.Background()
	storage := memstore.New()
	now := time.Now().UTC().Truncate(time.Second)
	if err := storage.CreateUser(ctx, &domain.User{UserID: "usr_ranked", DisplayName: "Ranked", AccountStatus: "active", LeaderboardVisibility: "public", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	watermark := now.Add(-10 * time.Second)
	snapshot := &domain.LeaderboardSnapshot{SnapshotID: "snap_watermark", BoardKey: "today", ScopeType: "global", ScopeKey: "all", MetricKey: "total_tokens", WindowStart: now.Add(-time.Hour), WindowEnd: now.Add(time.Second), TimezoneName: "UTC", RankingRuleVersion: 2, DataWatermarkAt: watermark, SnapshotStatus: "building", GeneratedAt: now}
	if err := storage.CreateSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := storage.CreateEntry(ctx, &domain.LeaderboardEntry{SnapshotID: snapshot.SnapshotID, RankNo: 1, UserID: "usr_ranked", MetricValue: 42, DisplayNameSnapshot: "Ranked"}); err != nil {
		t.Fatal(err)
	}
	if err := storage.PublishSnapshot(ctx, snapshot.SnapshotID, now); err != nil {
		t.Fatal(err)
	}

	handler := &Handler{Leaderboards: storage}
	request := httptest.NewRequest(http.MethodGet, "/v1/leaderboard?board=today", nil)
	response := httptest.NewRecorder()
	handler.HandleGetLeaderboard(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body LeaderboardSnapshotResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.DataWatermarkAt != watermark.Format(time.RFC3339) {
		t.Fatalf("dataWatermarkAt=%q want=%q", body.DataWatermarkAt, watermark.Format(time.RFC3339))
	}
	if body.LagSeconds < 10 || body.LagSeconds > 11 {
		t.Fatalf("lagSeconds=%d want 10..11", body.LagSeconds)
	}
}

func TestIngestHTTPDeadline(t *testing.T) {
	var deadline time.Time
	handler := withIngestDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		deadline, ok = r.Context().Deadline()
		if !ok {
			t.Fatal("ingest request context has no deadline")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	started := time.Now()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/telemetry/batches", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d", response.Code)
	}
	remaining := deadline.Sub(started)
	if remaining < 4*time.Second || remaining > ingestHTTPDeadline {
		t.Fatalf("deadline remaining=%s want about %s", remaining, ingestHTTPDeadline)
	}
}

func TestGzipDecompressedBodyLimits(t *testing.T) {
	for _, test := range []struct {
		name       string
		padding    int
		wantStatus int
	}{
		{name: "high compression body is valid", padding: 1 << 20, wantStatus: http.StatusOK},
		{name: "decompressed body over four MiB", padding: maxDecompressedIngestBodySize + 1, wantStatus: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			storage := memstore.New()
			now := time.Now().UTC()
			userID := "usr_gzip"
			installationID := "ins_01K4Z9X6Y7M8N9P0Q1R2S3T4V5"
			if err := storage.CreateUser(ctx, &domain.User{UserID: userID, DisplayName: "Gzip User", AccountStatus: "active", LeaderboardVisibility: "private", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			publicKey, _, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			if err := storage.CreateInstallation(ctx, &domain.Installation{InstallationID: installationID, UserID: userID, DevicePublicKey: publicKey, OSType: "windows", Architecture: "x86_64", CollectorVersion: "1.0.0", InstallationStatus: "active", RegisteredAt: now}); err != nil {
				t.Fatal(err)
			}
			handler := &Handler{Ingest: &ingest.Service{Batches: storage, Events: storage, Installs: storage, Users: storage}}
			batchID := "bat_01K4Z9X6Y7M8N9P0Q1R2S3T4V5"
			eventHash := sha256.Sum256([]byte(test.name))
			eventID := base64.RawURLEncoding.EncodeToString(eventHash[:])
			hmacValue := "hmac-sha256:" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
			body := `{"batchId":"` + batchID + `","installationId":"` + installationID + `","createdAt":"2026-08-30T00:00:00Z","events":[{"schemaVersion":"1.0","eventId":"` + eventID + `","adapterId":"dev.tokenshow.adapter.mock","adapterVersion":"1.0.0","agentId":"mock-agent","installationId":"` + installationID + `","occurredAt":"2026-08-30T00:00:00Z","source":{"kind":"jsonl_tail","cursorHmac":"` + hmacValue + `","rawFingerprintHmac":"` + hmacValue + `"},"accuracy":"exact","payload":{"type":"model_usage_recorded","providerId":"mock-provider","modelId":"mock-model","tokens":{"totalTokens":"15"}}}]} ` + strings.Repeat(" ", test.padding)
			var compressed bytes.Buffer
			zipper := gzip.NewWriter(&compressed)
			if _, err := zipper.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
			if err := zipper.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/telemetry/batches", bytes.NewReader(compressed.Bytes()))
			request.Header.Set("Content-Encoding", "gzip")
			bodyHash := sha256.Sum256(compressed.Bytes())
			request = request.WithContext(context.WithValue(context.WithValue(request.Context(), auth.InstallationIDKey, installationID), auth.BodyHashKey, bodyHash))
			response := httptest.NewRecorder()
			handler.HandleIngestBatch(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s compressed=%d decompressed=%d", response.Code, test.wantStatus, response.Body.String(), compressed.Len(), len(body))
			}
		})
	}
}

func TestRegisterThenSignedGzipBatchAndNonceReplay(t *testing.T) {
	ctx := context.Background()
	storage := memstore.New()
	sessionToken := "test-user-session-token"
	now := time.Now().UTC()
	if err := storage.CreateUser(ctx, &domain.User{
		UserID:                "usr_test",
		AuthSubjectHash:       sha256.Sum256([]byte(sessionToken)),
		DisplayName:           "Test User",
		AccountStatus:         "active",
		LeaderboardVisibility: "private",
		CreatedAt:             now,
		UpdatedAt:             now,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	deviceAuth := &auth.DeviceAuth{Installations: storage, Nonces: storage}
	handler := &Handler{
		Users:         storage,
		Installations: storage,
		Batches:       storage,
		Events:        storage,
		Leaderboards:  storage,
		Ingest: &ingest.Service{
			Batches: storage, Events: storage, Installs: storage, Users: storage,
		},
		DeviceAuth:   deviceAuth,
		UserSessions: &auth.StoredUserSessionResolver{Users: storage},
		IDGenerator:  func() string { return "ins_01K4Z9X6Y7M8N9P0Q1R2S3T4V5" },
	}
	server := httptest.NewServer(NewMux(handler))
	defer server.Close()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate device key: %v", err)
	}
	registrationBody, err := json.Marshal(map[string]string{
		"devicePublicKey":  base64.RawURLEncoding.EncodeToString(publicKey),
		"osType":           "windows",
		"architecture":     "x86_64",
		"collectorVersion": "0.1.0",
	})
	if err != nil {
		t.Fatalf("encode registration: %v", err)
	}
	registrationRequest, err := http.NewRequest(http.MethodPost, server.URL+"/v1/installations/register", bytes.NewReader(registrationBody))
	if err != nil {
		t.Fatalf("create registration request: %v", err)
	}
	registrationRequest.Header.Set("Authorization", "Bearer "+sessionToken)
	registrationRequest.Header.Set("Content-Type", "application/json")
	registrationResponse, err := server.Client().Do(registrationRequest)
	if err != nil {
		t.Fatalf("register device: %v", err)
	}
	defer registrationResponse.Body.Close()
	if registrationResponse.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(registrationResponse.Body)
		t.Fatalf("registration status = %d: %s", registrationResponse.StatusCode, body)
	}
	var registered RegisterInstallationResponse
	if err := json.NewDecoder(registrationResponse.Body).Decode(&registered); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if registered.InstallationID != "ins_01K4Z9X6Y7M8N9P0Q1R2S3T4V5" {
		t.Fatalf("installation id = %q", registered.InstallationID)
	}

	eventID := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	hmacValue := "hmac-sha256:" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	batchJSON := `{"batchId":"bat_01K4Z9X6Y7M8N9P0Q1R2S3T4V5","installationId":"` + registered.InstallationID + `","createdAt":"2026-08-30T00:00:00Z","events":[{"schemaVersion":"1.0","eventId":"` + eventID + `","adapterId":"dev.tokenshow.adapter.mock","adapterVersion":"1.0.0","agentId":"mock-agent","installationId":"` + registered.InstallationID + `","occurredAt":"2026-08-30T00:00:00Z","source":{"kind":"jsonl_tail","cursorHmac":"` + hmacValue + `","rawFingerprintHmac":"` + hmacValue + `"},"accuracy":"exact","payload":{"type":"model_usage_recorded","providerId":"mock-provider","modelId":"mock-model","tokens":{"totalTokens":"15"}}}]}`
	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	if _, err := zipper.Write([]byte(batchJSON)); err != nil {
		t.Fatalf("gzip batch: %v", err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatalf("close gzip batch: %v", err)
	}
	compressedBody := compressed.Bytes()
	headers, err := auth.SignRequest(
		registered.InstallationID,
		privateKey,
		http.MethodPost,
		"/v1/telemetry/batches",
		sha256.Sum256(compressedBody),
		bytes.Repeat([]byte{7}, 24),
	)
	if err != nil {
		t.Fatalf("sign batch: %v", err)
	}

	upload := func() *http.Response {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/telemetry/batches", bytes.NewReader(compressedBody))
		if err != nil {
			t.Fatalf("create upload request: %v", err)
		}
		request.Header = headers.Clone()
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Content-Encoding", "gzip")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatalf("upload batch: %v", err)
		}
		return response
	}

	first := upload()
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(first.Body)
		t.Fatalf("upload status = %d: %s", first.StatusCode, body)
	}
	var ack protocol.UploadAck
	if err := json.NewDecoder(first.Body).Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if ack.Accepted != 1 || ack.BatchID != "bat_01K4Z9X6Y7M8N9P0Q1R2S3T4V5" {
		t.Fatalf("unexpected ack: %#v", ack)
	}

	replay := upload()
	defer replay.Body.Close()
	if replay.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(replay.Body)
		t.Fatalf("replay status = %d: %s", replay.StatusCode, body)
	}
	replayBody, _ := io.ReadAll(replay.Body)
	if !strings.Contains(string(replayBody), "nonce replay detected") {
		t.Fatalf("replay response = %q", replayBody)
	}
}

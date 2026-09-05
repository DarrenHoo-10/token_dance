// checkproviders verifies deployment credentials without delivering any email.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"time"

	"tokendance/internal/config"
	"tokendance/internal/provider"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	storage, err := provider.NewObjectStorage(cfg)
	if err != nil {
		log.Fatal("Object storage configuration failed")
	}
	key := fmt.Sprintf("deployment-check/%d.txt", time.Now().UnixNano())
	payload := []byte("TokenDance deployment check")
	if err := storage.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), "text/plain"); err != nil {
		log.Fatal("Object storage write failed: ", err)
	}
	defer storage.DeleteObject(context.Background(), key)
	meta, err := storage.HeadObject(ctx, key)
	if err != nil || meta.Size != int64(len(payload)) {
		log.Fatal("Object storage HEAD failed")
	}
	signed, err := storage.PresignDownloadURL(ctx, key, time.Minute)
	if err != nil {
		log.Fatal("Object storage signing failed")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Get(signed)
	if err != nil {
		log.Fatal("Signed object download failed")
	}
	content, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !bytes.Equal(content, payload) {
		log.Fatal("Signed object contents did not match")
	}
	if err := storage.DeleteObject(ctx, key); err != nil {
		log.Fatal("Object storage cleanup failed")
	}
	log.Println("Object storage write, HEAD, signed download and delete passed")
	address := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if cfg.SMTPTLSMode == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		log.Fatal("SMTP connection failed")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	mail, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		log.Fatal("SMTP handshake failed")
	}
	defer mail.Close()
	if cfg.SMTPTLSMode == "starttls" {
		if err := mail.StartTLS(&tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			log.Fatal("SMTP STARTTLS failed")
		}
	}
	if err := mail.Auth(smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPHost)); err != nil {
		log.Fatal("SMTP authentication failed")
	}
	mail.Quit()
	log.Println("SMTP TLS and authentication passed; no email sent")
}

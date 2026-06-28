package asr

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"hikami-go/internal/config"
	"hikami-go/internal/session"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Publisher struct {
	client    *minio.Client
	bucket    string
	urlPrefix string
}

func NewS3Publisher(cfg *config.Config) (*S3Publisher, error) {
	s3cfg := cfg.ASRS3
	endpoint := strings.TrimSpace(s3cfg.Endpoint)

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("asr_s3: invalid endpoint %q: %w", endpoint, err)
	}
	host := u.Host
	if host == "" {
		host = endpoint
	}
	useSSL := u.Scheme == "https" || strings.HasPrefix(endpoint, "https://")

	opts := &minio.Options{
		Creds:        credentials.NewStaticV4(s3cfg.AccessKeyID, s3cfg.SecretResolved(), ""),
		Secure:       useSSL,
		Region:       strings.TrimSpace(s3cfg.Region),
		BucketLookup: minio.BucketLookupAuto,
	}
	if s3cfg.UsePathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}

	client, err := minio.New(host, opts)
	if err != nil {
		return nil, fmt.Errorf("asr_s3: create client failed: %w", err)
	}

	return &S3Publisher{
		client:    client,
		bucket:    strings.TrimSpace(s3cfg.Bucket),
		urlPrefix: trimRightSlash(strings.TrimSpace(s3cfg.PublicURLPrefix)),
	}, nil
}

func (p *S3Publisher) Publish(ctx context.Context, localAudio string, sessionInfo session.Session) (publicURL string, objectKey string, err error) {
	objectKey = s3ObjectKey(sessionInfo)

	info, err := p.client.FPutObject(ctx, p.bucket, objectKey, localAudio, minio.PutObjectOptions{
		ContentType: "audio/mpeg",
	})
	if err != nil {
		return "", "", fmt.Errorf("asr_s3: upload failed: %w", err)
	}
	slog.Debug("asr_s3: uploaded", "bucket", p.bucket, "key", objectKey, "size", info.Size)

	publicURL = s3PublicURL(p.urlPrefix, objectKey)
	return publicURL, objectKey, nil
}

func (p *S3Publisher) Delete(ctx context.Context, objectKey string) error {
	err := p.client.RemoveObject(ctx, p.bucket, objectKey, minio.RemoveObjectOptions{})
	if err != nil {
		slog.Warn("asr_s3: delete failed", "key", objectKey, "error", err)
		return err
	}
	slog.Debug("asr_s3: deleted", "bucket", p.bucket, "key", objectKey)
	return nil
}

func s3ObjectKey(si session.Session) string {
	return si.ChannelID + "/" + si.ID + "/audio.asr.mp3"
}

func s3PublicURL(prefix, key string) string {
	return trimRightSlash(prefix) + "/" + key
}

func trimRightSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

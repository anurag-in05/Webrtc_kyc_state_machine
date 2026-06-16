package recorder

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Uploader streams full_call.mp4 to S3 at finalize. Credentials resolve via the
// standard AWS chain (env, shared config, instance/IAM role). It is nil when
// AWS_S3_BUCKET is unset, which makes finalize keep the local path as the URL —
// invariant 4: a missing or failed upload degrades the recording, it never fails
// the call.
type S3Uploader struct {
	bucket   string
	folder   string // AWS_MEDIA_FOLDER, no surrounding slashes
	baseURL  string // AWS_URL, no trailing slash
	uploader *manager.Uploader
}

// NewS3Uploader builds the uploader, or returns nil if S3 is not configured (no
// bucket) or credentials/region fail to resolve — both fold to the local fallback.
func NewS3Uploader(bucket, folder, region, awsURL string) *S3Uploader {
	if bucket == "" {
		log.Print("recorder: AWS_S3_BUCKET unset; finalize will keep local URLs")
		return nil
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(region))
	if err != nil {
		log.Printf("recorder: AWS config load failed (%v); S3 disabled, using local URLs", err)
		return nil
	}
	return &S3Uploader{
		bucket:   bucket,
		folder:   strings.Trim(folder, "/"),
		baseURL:  strings.TrimRight(awsURL, "/"),
		uploader: manager.NewUploader(s3.NewFromConfig(cfg)),
	}
}

// upload streams localPath to s3://bucket/<key> and returns the AWS_URL-fronted
// URL. ContentType video/mp4 + SSE-AES256 — these are KYC recordings. The file
// handle is streamed (manager.Uploader chunks it), never read whole into memory.
func (u *S3Uploader) upload(ctx context.Context, sessionID, localPath string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	key := s3Key(u.folder, sessionID, "full_call.mp4")
	if _, err := u.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:               aws.String(u.bucket),
		Key:                  aws.String(key),
		Body:                 f,
		ContentType:          aws.String("video/mp4"),
		ServerSideEncryption: types.ServerSideEncryptionAes256,
	}); err != nil {
		return "", err
	}
	return s3URL(u.baseURL, key), nil
}

// s3Key mirrors brain/app/storage/s3.py:_key — "{folder}/{sid}/{filename}", the
// folder prefix omitted when empty — so recorder MP4s and brain transcripts land
// in the same per-session prefix.
func s3Key(folder, sessionID, filename string) string {
	if folder != "" {
		return fmt.Sprintf("%s/%s/%s", folder, sessionID, filename)
	}
	return fmt.Sprintf("%s/%s", sessionID, filename)
}

// s3URL mirrors brain/app/storage/s3.py:_url — "{AWS_URL}/{key}" with each path
// segment percent-encoded (AWS_MEDIA_FOLDER may contain spaces). Keys here are
// hex session ids + a fixed filename + a simple folder, for which url.PathEscape
// agrees with Python's urllib quote, so brain and recorder URLs stay identical.
func s3URL(baseURL, key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return baseURL + "/" + strings.Join(parts, "/")
}

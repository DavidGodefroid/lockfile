package source

import (
	"context"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Config struct {
	Bucket   string
	Region   string
	Endpoint string // S3-compatible store when set (path-style addressing)
	Prefix   string
}

type S3Creds struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

type s3Source struct {
	cfg    S3Config
	client *s3.Client
}

// NewS3 serves objects of one bucket under cfg.Prefix.
func NewS3(cfg S3Config, creds S3Creds) (Source, error) {
	opts := s3.Options{
		Region:      cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken),
	}
	if cfg.Endpoint != "" {
		opts.BaseEndpoint = aws.String(cfg.Endpoint)
		opts.UsePathStyle = true
	}
	return s3Source{cfg: cfg, client: s3.New(opts)}, nil
}

func (s s3Source) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	k, err := CheckKey(s.cfg.Prefix, key)
	if err != nil {
		return ObjectInfo{}, err
	}
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.cfg.Bucket, Key: &k})
	if err != nil {
		return ObjectInfo{}, wrap("s3 head object", err)
	}
	return ObjectInfo{Size: aws.ToInt64(out.ContentLength)}, nil
}

func (s s3Source) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	k, err := CheckKey(s.cfg.Prefix, key)
	if err != nil {
		return nil, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.cfg.Bucket, Key: &k})
	if err != nil {
		return nil, wrap("s3 get object", err)
	}
	return out.Body, nil
}

// Package testenv starts in-process protocol servers for tests: an S3 fake
// over HTTP and an SSH server with a read-only SFTP subsystem. Synthetic
// data only; nothing here is used outside tests.
package testenv

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// S3 is a running S3 fake with one bucket.
type S3 struct {
	Endpoint string
	Bucket   string
	admin    *s3.Client
}

// NewS3 starts an S3 fake with bucket.
func NewS3(t testing.TB, bucket string) *S3 {
	t.Helper()
	srv := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(srv.Close)
	admin := s3.New(s3.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("admin", "admin-secret", ""),
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
	})
	if _, err := admin.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	return &S3{Endpoint: srv.URL, Bucket: bucket, admin: admin}
}

// Put stores body at key.
func (s *S3) Put(t testing.TB, key string, body []byte) {
	t.Helper()
	if _, err := s.admin.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(s.Bucket), Key: aws.String(key), Body: bytes.NewReader(body),
	}); err != nil {
		t.Fatal(err)
	}
}

// SFTP is a running SFTP server.
type SFTP struct {
	Host       string
	Port       int
	User       string
	KnownHosts string // a known_hosts file holding the server's key
}

// SFTPOptions are the accepted logins: a password, an authorized key, or
// both.
type SFTPOptions struct {
	User          string
	Password      string
	AuthorizedKey ssh.PublicKey
}

// NewSFTP serves root read-only over SFTP.
func NewSFTP(t testing.TB, root string, o SFTPOptions) *SFTP {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{}
	if o.Password != "" {
		cfg.PasswordCallback = func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if c.User() == o.User && string(p) == o.Password {
				return nil, nil
			}
			return nil, errors.New("denied")
		}
	}
	if o.AuthorizedKey != nil {
		want := o.AuthorizedKey.Marshal()
		cfg.PublicKeyCallback = func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == o.User && bytes.Equal(k.Marshal(), want) {
				return nil, nil
			}
			return nil, errors.New("denied")
		}
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConn(nc, cfg, root)
		}
	}()
	addr := ln.Addr().String()
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, signer.PublicKey()) + "\n"
	if err := os.WriteFile(kh, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return &SFTP{Host: host, Port: p, User: o.User, KnownHosts: kh}
}

func serveConn(nc net.Conn, cfg *ssh.ServerConfig, root string) {
	defer nc.Close()
	_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		if nch.ChannelType() != "session" {
			nch.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, in, err := nch.Accept()
		if err != nil {
			return
		}
		go func() {
			for req := range in {
				ok := req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
				req.Reply(ok, nil)
				if ok {
					s, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(root), sftp.ReadOnly())
					if err == nil {
						s.Serve()
					}
					ch.Close()
				}
			}
		}()
	}
}

// ClientKey generates an ed25519 client key: its OpenSSH PEM, encrypted
// with passphrase when not empty, and its public key.
func ClientKey(t testing.TB, passphrase []byte) ([]byte, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var blk *pem.Block
	if len(passphrase) > 0 {
		blk, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "test", passphrase)
	} else {
		blk, err = ssh.MarshalPrivateKey(priv, "test")
	}
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(blk), sp
}

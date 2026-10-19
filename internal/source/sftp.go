package source

import (
	"context"
	"errors"
	"io"
	"net"
	"path"
	"strconv"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type SFTPConfig struct {
	Host       string
	Port       int
	User       string
	BaseDir    string
	KnownHosts string // path; strict, an unknown host key is refused
}

type SFTPCreds struct {
	Password   string
	PrivateKey []byte
}

type sftpSource struct {
	cfg  SFTPConfig
	ssh  *ssh.ClientConfig
	addr string
}

const sftpDialTimeout = 15 * time.Second

// NewSFTP serves files under cfg.BaseDir of an SFTP server whose host key
// must be in cfg.KnownHosts.
func NewSFTP(cfg SFTPConfig, creds SFTPCreds) (Source, error) {
	if cfg.KnownHosts == "" {
		return nil, wrap("sftp", errors.New("known_hosts is required"))
	}
	hk, err := knownhosts.New(cfg.KnownHosts)
	if err != nil {
		return nil, wrap("sftp known_hosts", err)
	}
	var auth []ssh.AuthMethod
	if len(creds.PrivateKey) > 0 {
		signer, err := ssh.ParsePrivateKey(creds.PrivateKey)
		if err != nil {
			return nil, wrap("sftp private key", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if creds.Password != "" {
		auth = append(auth, ssh.Password(creds.Password))
	}
	if len(auth) == 0 {
		return nil, wrap("sftp", errors.New("no credentials"))
	}
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	return sftpSource{
		cfg:  cfg,
		ssh:  &ssh.ClientConfig{User: cfg.User, Auth: auth, HostKeyCallback: hk, Timeout: sftpDialTimeout},
		addr: net.JoinHostPort(cfg.Host, strconv.Itoa(port)),
	}, nil
}

type sftpConn struct {
	ssh  *ssh.Client
	sftp *sftp.Client
}

func (c sftpConn) Close() error {
	return errors.Join(c.sftp.Close(), c.ssh.Close())
}

func (s sftpSource) dial(ctx context.Context) (sftpConn, error) {
	d := net.Dialer{Timeout: sftpDialTimeout}
	nc, err := d.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return sftpConn{}, wrap("sftp dial", err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(nc, s.addr, s.ssh)
	if err != nil {
		nc.Close()
		return sftpConn{}, wrap("sftp handshake", err)
	}
	sc := ssh.NewClient(cc, chans, reqs)
	fc, err := sftp.NewClient(sc)
	if err != nil {
		sc.Close()
		return sftpConn{}, wrap("sftp session", err)
	}
	return sftpConn{ssh: sc, sftp: fc}, nil
}

func (s sftpSource) path(key string) (string, error) {
	k, err := CheckKey("", key)
	if err != nil {
		return "", err
	}
	return path.Join(s.cfg.BaseDir, k), nil
}

func (s sftpSource) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	p, err := s.path(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	c, err := s.dial(ctx)
	if err != nil {
		return ObjectInfo{}, err
	}
	defer c.Close()
	fi, err := c.sftp.Stat(p)
	if err != nil {
		return ObjectInfo{}, wrap("sftp stat", err)
	}
	if !fi.Mode().IsRegular() {
		return ObjectInfo{}, wrap("sftp stat", errors.New("not a regular file"))
	}
	return ObjectInfo{Size: fi.Size()}, nil
}

type sftpFile struct {
	*sftp.File
	conn sftpConn
}

func (f sftpFile) Close() error { return errors.Join(f.File.Close(), f.conn.Close()) }

func (s sftpSource) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	c, err := s.dial(ctx)
	if err != nil {
		return nil, err
	}
	f, err := c.sftp.Open(p)
	if err != nil {
		c.Close()
		return nil, wrap("sftp open", err)
	}
	return sftpFile{File: f, conn: c}, nil
}

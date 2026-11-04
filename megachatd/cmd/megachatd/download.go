package main

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// DownloadPort is the port of the temporary download server
// (protocols/download.py DOWNLOAD_PORT).
const DownloadPort = 10042

// downloader is protocols/download.py: a persistent plain HTTP file server on
// 0.0.0.0:10042 over <data>/downloads that holds one file at a time, started
// on first use. It is deliberately unauthenticated, like Python's, so that a
// remote terminal can fetch the URL with curl.
type downloader struct {
	dir  string
	port int

	mu   sync.Mutex
	base string
	srv  *http.Server
}

func newDownloader(dir string) *downloader { return &downloader{dir: dir, port: DownloadPort} }

func localIP() string {
	if conn := os.Getenv("SSH_CONNECTION"); conn != "" {
		if f := strings.Fields(conn); len(f) >= 3 {
			return f[2]
		}
	}
	if c, err := net.Dial("udp", "10.255.255.255:1"); err == nil {
		defer c.Close()
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
			return a.IP.String()
		}
	}
	return "127.0.0.1"
}

func (d *downloader) ensure() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.srv != nil {
		return d.base, nil
	}
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(d.port)))
	if err != nil {
		return "", err
	}
	fs := http.FileServer(http.Dir(d.dir))
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r) // no directory listing
			return
		}
		fs.ServeHTTP(w, r)
	})}
	go func() { _ = srv.Serve(ln) }()
	d.srv = srv
	d.base = "http://" + net.JoinHostPort(localIP(), strconv.Itoa(d.port))
	return d.base, nil
}

func (d *downloader) clean() {
	entries, _ := os.ReadDir(d.dir)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(d.dir, e.Name()))
	}
}

func safeName(name string) string {
	name = filepath.Base(name)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return "download"
	}
	return name
}

// ServeFilePath implements media.Downloader.
func (d *downloader) ServeFilePath(path string) (string, error) {
	base, err := d.ensure()
	if err != nil {
		return "", err
	}
	d.clean()
	name := safeName(path)
	link := filepath.Join(d.dir, name)
	if err := os.Symlink(path, link); err != nil {
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return "", rerr
		}
		if werr := os.WriteFile(link, data, 0o644); werr != nil {
			return "", werr
		}
	}
	return base + "/" + name, nil
}

// ServeTextAsFile implements media.Downloader.
func (d *downloader) ServeTextAsFile(text, filename string) (string, error) {
	base, err := d.ensure()
	if err != nil {
		return "", err
	}
	d.clean()
	name := safeName(filename)
	if err := os.WriteFile(filepath.Join(d.dir, name), []byte(text), 0o644); err != nil {
		return "ERROR: Cannot write temp file: " + err.Error(), nil
	}
	return base + "/" + name, nil
}

func (d *downloader) Close() {
	d.mu.Lock()
	srv := d.srv
	d.srv = nil
	d.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
}

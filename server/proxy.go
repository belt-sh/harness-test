package server

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// StartProxy starts an HTTP CONNECT proxy on a random port. Agents that set
// HTTPS_PROXY to this address will have their TLS connections MITM'd: the proxy
// terminates TLS using the CA cert from StartIntercept, then routes the
// decrypted HTTP requests through the mock server's mux.
//
// Must be called after StartIntercept (needs the CA-signed TLS cert).
func (s *MockServer) StartProxy() (string, error) {
	if len(s.tlsCerts) == 0 {
		return "", fmt.Errorf("StartIntercept must be called first")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	s.proxyListener = ln

	go s.acceptProxy(ln)
	return fmt.Sprintf("http://%s", ln.Addr()), nil
}

func (s *MockServer) acceptProxy(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handleProxyConn(conn)
	}
}

func (s *MockServer) handleProxyConn(conn net.Conn) {
	defer conn.Close()

	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	if req.Method != "CONNECT" {
		s.serveProxiedHTTP(conn, req)
		return
	}

	host := req.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	hostname, _, _ := net.SplitHostPort(host)

	if !isInterceptedHost(hostname) {
		fmt.Fprintf(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}

	fmt.Fprintf(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")

	tlsConn := tls.Server(conn, &tls.Config{
		Certificates: s.tlsCerts,
	})
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	defer tlsConn.Close()

	innerReq, err := http.ReadRequest(bufio.NewReader(tlsConn))
	if err != nil {
		return
	}
	innerReq.URL.Host = hostname
	innerReq.URL.Scheme = "https"

	rw := &connResponseWriter{conn: tlsConn, header: make(http.Header), code: 200}
	s.srv.Handler.ServeHTTP(rw, innerReq)
	rw.finish()
}

func (s *MockServer) serveProxiedHTTP(conn net.Conn, req *http.Request) {
	rw := &connResponseWriter{conn: conn, header: make(http.Header), code: 200}
	s.srv.Handler.ServeHTTP(rw, req)
	rw.finish()
}

var interceptedHosts map[string]struct{}

func init() {
	interceptedHosts = make(map[string]struct{}, len(LLMHosts))
	for _, h := range LLMHosts {
		interceptedHosts[h] = struct{}{}
	}
}

func isInterceptedHost(hostname string) bool {
	_, ok := interceptedHosts[hostname]
	return ok
}

// connResponseWriter implements http.ResponseWriter over a raw net.Conn.
type connResponseWriter struct {
	conn   net.Conn
	header http.Header
	code   int
	wrote  bool
}

func (w *connResponseWriter) Header() http.Header {
	return w.header
}

func (w *connResponseWriter) WriteHeader(code int) {
	w.code = code
}

func (w *connResponseWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.sendHeader()
	}
	return w.conn.Write(b)
}

func (w *connResponseWriter) Flush() {}

func (w *connResponseWriter) sendHeader() {
	w.wrote = true
	fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n", w.code, http.StatusText(w.code))
	w.header.Write(w.conn)
	io.WriteString(w.conn, "\r\n")
}

func (w *connResponseWriter) finish() {
	if !w.wrote {
		w.sendHeader()
	}
}

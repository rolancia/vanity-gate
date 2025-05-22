package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/samber/lo"

	"github.com/rolancia/vanity-gate/lib/config"
	"github.com/rolancia/vanity-gate/lib/control"
)

func main() {
	appCtx := context.Background()
	appCtx, cancel := context.WithCancel(appCtx)
	defer cancel()

	ctl := control.CreateManager(appCtx, time.Now())

	go func() {
		err := http.ListenAndServe(fmt.Sprintf(":%d", 11994), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/config" {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

				// OPTIONS 프리플라이트 요청일 경우
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusOK)
					return
				} else if r.Method == http.MethodPut {
					// parse body config from json
					var cfg config.Config
					if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
						http.Error(w, "bad Request", http.StatusBadRequest)
						return
					}
					config.SetConfig(cfg)
					w.WriteHeader(http.StatusOK)
					return
				}
			}

			reqApp := r.URL.Query().Get("vanity_app")
			// if no app is specified, use last app
			if reqApp == "" {
				reqApp = ctl.LastApp()
			}

			cfg := config.GetConfig()
			app, ok := lo.Find(cfg.Apps, func(p config.App) bool {
				reqAppName := reqApp
				return p.Name == reqAppName
			})
			if !ok {
				http.NotFound(w, r)
				return
			}

			chResult := make(chan control.Result)
			parsed, err := url.Parse(app.URL)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			ctl.Queue(appCtx, app.Name, app.Entrypoint, parsed.Host, chResult, time.Now())
			result := <-chResult
			if result.Err != nil {
				if result.ErrCode == http.StatusInternalServerError {
					fmt.Println(result.Err)
					http.Error(w, "", result.ErrCode)
				} else {
					fmt.Println(result.Err)
					http.Error(w, result.Err.Error(), result.ErrCode)
				}

				return
			}

			proxy(app.URL, w, r, app.Name)
		}))
		if err != nil {
			panic(err)
		}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	ctl.Stop()
}

var proxyTransport = &http.Transport{
	ResponseHeaderTimeout: 30 * time.Second,
	IdleConnTimeout:       90 * time.Second,
	DialContext: (&net.Dialer{
		Timeout: 30 * time.Second,
	}).DialContext,
}

func proxy(u string, w http.ResponseWriter, r *http.Request, app string) {
	base, _ := url.Parse(u)
	target := base.ResolveReference(r.URL)

	if strings.EqualFold(r.Header.Get("Connection"), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		proxyWebsocket(w, r, target.Host)
		return
	}

	clientETag := r.Header.Get("If-None-Match")

	proxy := httputil.NewSingleHostReverseProxy(base)
	proxy.Transport = proxyTransport
	proxy.ModifyResponse = func(resp *http.Response) error {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		resp.Body.Close()
		sum := sha256.Sum256(body)
		newETag := `"` + hex.EncodeToString(sum[:]) + `"`

		hdr := resp.Header
		hdr.Set("ETag", newETag)
		hdr.Set("Cache-Control", "max-age=0, must-revalidate")

		if clientETag != "" && clientETag == newETag {
			resp.StatusCode = http.StatusNotModified
			resp.Body = http.NoBody
			hdr.Del("Content-Type")
			hdr.Del("Content-Length")
			return nil
		}

		resp.Body = io.NopCloser(bytes.NewReader(body))
		hdr.Set("Content-Length", strconv.Itoa(len(body)))
		return nil
	}
	proxy.ServeHTTP(w, r)
}

func proxyWebsocket(w http.ResponseWriter, r *http.Request, target string) {
	// dial backend
	backendConn, err := net.Dial("tcp", target)
	if err != nil {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	// hijack client
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijack not supported", http.StatusInternalServerError)
		return
	}
	clientConn, buf, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()
	defer backendConn.Close()

	// write the raw upgrade request to backend
	r.Write(backendConn)

	// now copy bytes both ways
	go io.Copy(backendConn, buf)        // any buffered data
	go io.Copy(backendConn, clientConn) // rest of client→backend
	io.Copy(clientConn, backendConn)    // backend→client
}

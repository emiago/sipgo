package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"

	"github.com/icholy/digest"
)

func main() {
	extIP := flag.String("ip", "127.0.0.1:5060", "My exernal ip")
	creds := flag.String("u", "alice:alice", "Coma seperated username:password list")
	tran := flag.String("t", "udp", "Transport")
	tlskey := flag.String("tlskey", "", "TLS key path")
	tlscrt := flag.String("tlscrt", "", "TLS crt path")
	flag.Parse()

	// Make SIP Debugging available
	sip.SIPDebug = os.Getenv("SIP_DEBUG") != ""

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))

	registry := make(map[string]string)
	for _, c := range strings.Split(*creds, ",") {
		arr := strings.Split(c, ":")
		registry[arr[0]] = arr[1]
	}

	ua, err := sipgo.NewUA(
		sipgo.WithUserAgent("SIPGO"),
		// sipgo.WithUserAgentIP(*extIP),
	)
	if err != nil {
		slog.Error("Fail to setup user agent", "error", err)
		os.Exit(1)
	}

	srv, err := sipgo.NewServer(ua)
	if err != nil {
		slog.Error("Fail to setup server handle", "error", err)
		os.Exit(1)
	}

	ctx := context.TODO()

	// NOTE: This server only supports 1 REGISTRATION/Chalenge
	// This needs to be rewritten in better way
	var chal digest.Challenge
	srv.OnRegister(func(req *sip.Request, tx sip.ServerTransaction) {
		// https://www.rfc-editor.org/rfc/rfc2617#page-6
		h := req.GetHeader("Authorization")
		if h == nil {
			chal = digest.Challenge{
				Realm:     "sipgo-server",
				Nonce:     fmt.Sprintf("%d", time.Now().UnixMicro()),
				Opaque:    "sipgo",
				Algorithm: "MD5",
			}

			res := sip.NewResponseFromRequest(req, 401, "Unathorized", nil)
			res.AppendHeader(sip.NewHeader("WWW-Authenticate", chal.String()))

			tx.Respond(res)
			return
		}

		cred, err := digest.ParseCredentials(h.Value())
		if err != nil {
			slog.Error("parsing creds failed", "error", err)
			tx.Respond(sip.NewResponseFromRequest(req, 401, "Bad credentials", nil))
			return
		}

		// Check registry
		passwd, exists := registry[cred.Username]
		if !exists {
			tx.Respond(sip.NewResponseFromRequest(req, 404, "Bad authorization header", nil))
			return
		}

		// Make digest and compare response
		digCred, err := digest.Digest(&chal, digest.Options{
			Method:   "REGISTER",
			URI:      cred.URI,
			Username: cred.Username,
			Password: passwd,
		})

		if err != nil {
			slog.Error("Calc digest failed", "error", err)
			tx.Respond(sip.NewResponseFromRequest(req, 401, "Bad credentials", nil))
			return
		}

		if cred.Response != digCred.Response {
			tx.Respond(sip.NewResponseFromRequest(req, 401, "Unathorized", nil))
			return
		}
		slog.Info("New client registered", "username", cred.Username, "source", req.Source())
		tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
	})

	slog.Info("Listening on", "addr", *extIP)

	go func() {
		http.ListenAndServe(":6060", nil)
	}()

	switch *tran {
	case "tls", "wss":
		cert, err := tls.LoadX509KeyPair(*tlscrt, *tlskey)
		if err != nil {

			slog.Error("Fail to load  x509 key and crt", "error", err)
			os.Exit(1)
		}
		if err := srv.ListenAndServeTLS(ctx, *tran, *extIP, &tls.Config{Certificates: []tls.Certificate{cert}}); err != nil {
			slog.Info("Listening stop", "error", err)
		}
		return
	}

	if err := srv.ListenAndServe(ctx, *tran, *extIP); err != nil {
		slog.Error("Failed to listen", "error", err)
	}
}

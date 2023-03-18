package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/transport"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/icholy/digest"
)

func main() {
	extIP := flag.String("ip", "127.0.0.1:5060", "My exernal ip")
	creds := flag.String("u", "alice:alice", "Coma seperated username:password list")
	sipdebug := flag.Bool("sipdebug", false, "Turn on sipdebug")
	flag.Parse()

	// Make SIP Debugging available
	transport.SIPDebug = *sipdebug

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMicro
	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: time.StampMicro,
	}).With().Timestamp().Logger().Level(zerolog.DebugLevel)

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
		log.Fatal().Err(err).Msg("Fail to setup user agent")
	}

	srv, err := sipgo.NewServer(ua)
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to setup server handle")
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
			log.Error().Err(err).Msg("parsing creds failed")
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
			log.Error().Err(err).Msg("Calc digest failed")
			tx.Respond(sip.NewResponseFromRequest(req, 401, "Bad credentials", nil))
			return
		}

		if cred.Response != digCred.Response {
			tx.Respond(sip.NewResponseFromRequest(req, 401, "Unathorized", nil))
			return
		}
		log.Info().Str("username", cred.Username).Str("source", req.Source()).Msg("New client registered")
		tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
	})

	srv.ListenAndServe(ctx, "udp", *extIP)
}

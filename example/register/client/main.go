package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/parser"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/transport"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/icholy/digest"
)

func main() {
	extIP := flag.String("ip", "127.0.0.50:5060", "My exernal ip")
	dst := flag.String("srv", "127.0.0.1:5060", "Destination")
	tran := flag.String("t", "udp", "Transport")
	username := flag.String("u", "alice", "SIP Username")
	password := flag.String("p", "alice", "Password")
	flag.Parse()

	// Make SIP Debugging available
	transport.SIPDebug = os.Getenv("SIP_DEBUG") != ""

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMicro
	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: time.StampMicro,
	}).With().Timestamp().Logger().Level(zerolog.InfoLevel)

	if lvl, err := zerolog.ParseLevel(os.Getenv("LOG_LEVEL")); err == nil && lvl != zerolog.NoLevel {
		log.Logger = log.Logger.Level(lvl)
	}

	// Setup UAC
	ua, err := sipgo.NewUA(
		sipgo.WithUserAgent(*username),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to setup user agent")
	}

	srv, err := sipgo.NewServer(ua)
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to setup server handle")
	}

	client, err := sipgo.NewClient(ua, sipgo.WithClientAddr(*extIP))
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to setup client handle")
	}

	ctx := context.TODO()
	go srv.ListenAndServe(ctx, *tran, *extIP)

	// Wait that ouir server loads
	time.Sleep(1 * time.Second)
	log.Info().Str("addr", *extIP).Msg("Server listening on")

	// Create basic REGISTER request structure
	recipient := &sip.Uri{}
	parser.ParseUri(fmt.Sprintf("sip:%s@%s", *username, *dst), recipient)
	req := sip.NewRequest(sip.REGISTER, recipient)
	req.AppendHeader(
		sip.NewHeader("Contact", fmt.Sprintf("<sip:%s@%s>", *username, *extIP)),
	)
	req.SetTransport(strings.ToUpper(*tran))

	// Send request and parse response
	// req.SetDestination(*dst)
	log.Info().Msg(req.StartLine())
	tx, err := client.TransactionRequest(req.Clone())
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to create transaction")
	}
	defer tx.Terminate()

	res, err := getResponse(tx)
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to get response")
	}

	log.Info().Int("status", int(res.StatusCode())).Msg("Received status")
	if res.StatusCode() == 401 {
		// Get WwW-Authenticate
		wwwAuth := res.GetHeader("WWW-Authenticate")
		chal, err := digest.ParseChallenge(wwwAuth.Value())
		if err != nil {
			log.Fatal().Str("wwwauth", wwwAuth.Value()).Err(err).Msg("Fail to parse challenge")
		}

		// Reply with digest
		cred, _ := digest.Digest(chal, digest.Options{
			Method:   req.Method.String(),
			URI:      recipient.Host,
			Username: *username,
			Password: *password,
		})

		newReq := req.Clone()
		newReq.AppendHeader(sip.NewHeader("Authorization", cred.String()))

		tx, err = client.TransactionRequest(newReq)
		if err != nil {
			log.Fatal().Err(err).Msg("Fail to create transaction")
		}
		defer tx.Terminate()

		res, err = getResponse(tx)
		if err != nil {
			log.Fatal().Err(err).Msg("Fail to get response")
		}
	}

	if res.StatusCode() != 200 {
		log.Fatal().Msg("Fail to register")
	}

	log.Info().Msg("Client registered")
}

func getResponse(tx sip.ClientTransaction) (*sip.Response, error) {
	select {
	case <-tx.Done():
		return nil, fmt.Errorf("transaction died")
	case res := <-tx.Responses():
		return res, nil
	}
}

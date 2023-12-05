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
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/icholy/digest"
)

func main() {
	inter := flag.String("h", "localhost", "My interface ip or hostname")
	dst := flag.String("srv", "127.0.0.1:5060", "Destination")
	tran := flag.String("t", "udp", "Transport")
	username := flag.String("u", "alice", "SIP Username")
	password := flag.String("p", "alice", "Password")
	flag.Parse()

	// Make SIP Debugging available
	sip.SIPDebug = os.Getenv("SIP_DEBUG") != ""

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

	client, err := sipgo.NewClient(ua, sipgo.WithClientHostname(*inter))
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to setup client handle")
	}
	defer client.Close()

	// Create basic REGISTER request structure
	recipient := &sip.Uri{}
	sip.ParseUri(fmt.Sprintf("sip:%s@%s", *username, *dst), recipient)
	req := sip.NewRequest(sip.REGISTER, recipient)
	req.AppendHeader(
		sip.NewHeader("Contact", fmt.Sprintf("<sip:%s@%s>", *username, *inter)),
	)
	req.SetTransport(strings.ToUpper(*tran))

	// Send request and parse response
	// req.SetDestination(*dst)
	log.Info().Msg(req.StartLine())
	ctx := context.Background()
	tx, err := client.TransactionRequest(ctx, req)
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to create transaction")
	}
	defer tx.Terminate()

	res, err := getResponse(tx)
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to get response")
	}

	log.Info().Int("status", int(res.StatusCode)).Msg("Received status")
	if res.StatusCode == 401 {
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

		ctx := context.Background()
		tx, err := client.TransactionRequest(ctx, newReq)
		if err != nil {
			log.Fatal().Err(err).Msg("Fail to create transaction")
		}
		defer tx.Terminate()

		res, err = getResponse(tx)
		if err != nil {
			log.Fatal().Err(err).Msg("Fail to get response")
		}
	}

	if res.StatusCode != 200 {
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

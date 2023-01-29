package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgo/transport"

	_ "net/http/pprof"

	"github.com/emiago/sipgo"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	// _ "go.uber.org/automaxprocs"
)

var ()

func main() {
	debflag := flag.Bool("debug", false, "")
	pprof := flag.Bool("pprof", false, "Full profile")
	extIP := flag.String("ip", "127.0.0.1:5060", "My exernal ip")
	dst := flag.String("dst", "", "Destination pbx, sip server")
	transportType := flag.String("t", "udp", "Transport, default will be determined by request")
	flag.Parse()

	if *pprof {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
		runtime.MemProfileRate = 64
	}

	lev := zerolog.InfoLevel
	if *debflag {
		lev = zerolog.DebugLevel
		transport.SIPDebug = true
	}

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMicro
	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: time.StampMicro,
	}).With().Timestamp().Logger().Level(lev)

	log.Info().Int("cpus", runtime.NumCPU()).Msg("Runtime")
	log.Info().Msg("Server routes setuped")
	go httpServer(":8080")

	srv := setupSipProxy(*dst, *extIP)
	if err := srv.ListenAndServe(context.TODO(), *transportType, *extIP); err != nil {
		log.Error().Err(err).Msg("Fail to start sip server")
		return
	}
}

func httpServer(address string) {
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("Alive"))
	})

	http.HandleFunc("/mem", func(w http.ResponseWriter, r *http.Request) {
		runtime.GC()
		stats := &runtime.MemStats{}
		runtime.ReadMemStats(stats)
		data, _ := json.MarshalIndent(stats, "", "  ")
		w.WriteHeader(200)
		w.Write(data)
	})

	log.Info().Msgf("Http server started address=%s", address)
	http.ListenAndServe(address, nil)
}

func setupSipProxy(proxydst string, ip string) *sipgo.Server {
	// Prepare all variables we need for our service
	host, port, _ := sip.ParseAddr(ip)
	ua, err := sipgo.NewUA(
		sipgo.WithUserAgentIP(ip),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to setup user agent")
	}

	srv, err := sipgo.NewServer(ua)
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to setup server handle")
	}

	client, err := sipgo.NewClient(ua)
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to setup client handle")
	}

	registry := NewRegistry()
	var getDestination = func(req *sip.Request) string {
		tohead, _ := req.To()
		dst := registry.Get(tohead.Address.User)

		if dst == "" {
			return proxydst
		}

		return dst
	}

	var reply = func(tx sip.ServerTransaction, req *sip.Request, code sip.StatusCode, reason string) {
		resp := sip.NewResponseFromRequest(req, code, reason, nil)
		resp.SetDestination(req.Source()) //This is optional, but can make sure not wrong via is read
		if err := tx.Respond(resp); err != nil {
			log.Error().Err(err).Msg("Fail to respond on transaction")
		}
	}

	var route = func(req *sip.Request, tx sip.ServerTransaction) {
		// If we are proxying to asterisk or other proxy -dst must be set
		// Otherwise we will look on our registration entries
		dst := getDestination(req)

		if dst == "" {
			reply(tx, req, 404, "Not found")
			return
		}

		req.SetDestination(dst)
		// Start client transaction and relay our request
		clTx, err := client.TransactionRequest(req, sipgo.ClientRequestAddVia, sipgo.ClientRequestAddRecordRoute)
		if err != nil {
			log.Error().Err(err).Msg("RequestWithContext  failed")
			reply(tx, req, 500, "")
			return
		}
		defer clTx.Terminate()

		// Keep monitoring transactions, and proxy client responses to server transaction
		log.Debug().Str("req", req.Method().String()).Msg("Starting transaction")
		for {
			select {

			case res, more := <-clTx.Responses():
				if !more {
					return
				}

				res.SetDestination(req.Source())
				sipgo.ClientResponseRemoveVia(client, res) // For now this needs to be called manually
				if err := tx.Respond(res); err != nil {
					log.Error().Err(err).Msg("ResponseHandler transaction respond failed")
				}

				// Early terminate
				if req.Method() == sip.BYE {
					// We will call client Terminate
					return
				}

			case m := <-tx.Acks():
				// Acks can not be send directly trough destination
				log.Info().Str("m", m.StartLine()).Str("dst", dst).Msg("Proxing ACK")
				m.SetDestination(dst)
				client.WriteRequest(m)

			case m := <-tx.Cancels():
				// Send response imediatelly
				reply(tx, m, 200, "OK")
				// Cancel client transacaction without waiting. This will send CANCEL request
				clTx.Cancel()

			case err := <-clTx.Errors():
				log.Error().Err(err).Str("caller", req.Method().String()).Msg("Client Transaction Error")
				return

			case err := <-tx.Errors():
				log.Error().Err(err).Str("caller", req.Method().String()).Msg("Server transaction error")
				return

			case <-tx.Done():
				log.Debug().Str("req", req.Method().String()).Msg("Transaction done")
				return
			}
		}
	}

	var registerHandler = func(req *sip.Request, tx sip.ServerTransaction) {
		// https://www.rfc-editor.org/rfc/rfc3261#section-10.3
		cont, exists := req.Contact()
		if !exists {
			reply(tx, req, 404, "Missing address of record")
			return
		}

		// We have a list of uris
		uri := cont.Address
		if uri.Host == host && uri.Port == port {
			reply(tx, req, 401, "Contact address not provided")
			return
		}

		addr := uri.Host + ":" + strconv.Itoa(uri.Port)

		registry.Add(uri.User, addr)
		log.Debug().Msgf("Contact added %s -> %s", uri.User, addr)

		res := sip.NewResponseFromRequest(req, 200, "OK", nil)
		// log.Debug().Msgf("Sending response: \n%s", res.String())

		// URI params must be reset or this should be regenetad
		cont.Address.UriParams = sip.NewParams()
		cont.Address.UriParams.Add("transport", req.Transport())

		if err := tx.Respond(res); err != nil {
			log.Error().Err(err).Msg("Sending REGISTER OK failed")
			return
		}
	}

	var inviteHandler = func(req *sip.Request, tx sip.ServerTransaction) {
		route(req, tx)
	}

	var ackHandler = func(req *sip.Request, tx sip.ServerTransaction) {
		dst := getDestination(req)
		req.SetDestination(dst)
		if err := client.WriteRequest(req, sipgo.ClientRequestAddVia); err != nil {
			log.Error().Err(err).Msg("Send failed")
			reply(tx, req, 500, "")
		}
	}

	var cancelHandler = func(req *sip.Request, tx sip.ServerTransaction) {
		route(req, tx)
	}

	var byeHandler = func(req *sip.Request, tx sip.ServerTransaction) {
		route(req, tx)
	}

	srv.OnRegister(registerHandler)
	srv.OnInvite(inviteHandler)
	srv.OnAck(ackHandler)
	srv.OnCancel(cancelHandler)
	srv.OnBye(byeHandler)
	return srv
}

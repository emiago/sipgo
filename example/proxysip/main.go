package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"github.com/emiraganov/sipgo/sip"

	_ "net/http/pprof"

	"github.com/emiraganov/sipgo"

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
	dst := flag.String("dst", "127.0.0.2:5060", "Destination pbx, sip server")
	transportType := flag.String("t", "udp", "Transport, default will be determined by request")
	flag.Parse()

	if *pprof {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
		runtime.MemProfileRate = 64
	}

	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "2006-01-02 15:04:05.000",
	}).With().Timestamp().Logger().Level(zerolog.InfoLevel)

	if *debflag {
		log.Logger = log.Logger.Level(zerolog.DebugLevel)
	}

	log.Info().Int("cpus", runtime.NumCPU()).Msg("Runtime")
	log.Info().Msg("Server routes setuped")
	go httpServer(":8080")

	srv := setupSipProxy(*dst, *extIP)
	// Add listener
	srv.Listen(*transportType, *extIP)
	if err := srv.Serve(); err != nil {
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
	srv, _ := sipgo.NewServer(
		sipgo.WithIP(ip),
	)
	registry := NewRegistry()

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
		if proxydst == "" {
			tohead, _ := req.To()
			proxydst = registry.Get(tohead.Address.User)
		}

		if proxydst == "" {
			reply(tx, req, 404, "Not found")
			return
		}

		req.SetDestination(proxydst)
		// Append our via
		if via, exists := req.Via(); exists {
			newvia := via.Clone()
			newvia.Host = host
			newvia.Port = port
			req.PrependHeader(newvia) //Via should be rewriten by transport
		}

		// Start client transaction and relay our request
		clTx, err := srv.TransactionRequest(req)
		if err != nil {
			log.Error().Err(err).Msg("RequestWithContext  failed")
			reply(tx, req, 500, "")
			return
		}
		defer clTx.Terminate()

		// Keep monitoring transactions, and proxy client responses to server transaction
		for {
			select {

			case res, more := <-clTx.Responses():
				if !more {
					return
				}
				res.RemoveHeader("via")

				res.SetDestination(req.Source())
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
				m.SetDestination(proxydst)
				srv.Send(m)

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
				log.Debug().Msg("Transaction done")
				return
			}
		}
	}

	var registerHandler = func(req *sip.Request, tx sip.ServerTransaction) {
		cont, exists := req.Contact()
		if !exists {
			reply(tx, req, 500, "Missing contact")
			return
		}

		// We have a list of uris
		uri := cont.Address
		addr := uri.Host + strconv.Itoa(uri.Port)
		registry.Add(uri.User, addr)
		log.Debug().Msgf("Contact added %s -> %s", uri.User, addr)

		res := sip.NewResponseFromRequest(req, 200, "OK", nil)
		// log.Debug().Msgf("Sending response: \n%s", res.String())
		if err := tx.Respond(res); err != nil {
			log.Error().Err(err).Msg("Sending REGISTER OK failed")
			return
		}
	}

	var inviteHandler = func(req *sip.Request, tx sip.ServerTransaction) {
		route(req, tx)
	}

	var ackHandler = func(req *sip.Request, tx sip.ServerTransaction) {
		req.SetDestination(proxydst)
		if err := srv.Send(req); err != nil {
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

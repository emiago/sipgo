package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"strconv"

	"github.com/arl/statsviz"

	"github.com/emiago/sipgo/sip"

	"github.com/emiago/sipgo"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	// _ "go.uber.org/automaxprocs"
)

var ()

func main() {
	defer pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)

	debflag := flag.Bool("debug", false, "")
	pprof := flag.Bool("pprof", false, "Full profile")
	extIP := flag.String("ip", "127.0.0.1:5060", "My exernal ip")
	dst := flag.String("dst", "", "Destination pbx, sip server")
	transportType := flag.String("t", "udp", "Transport, default will be determined by request")
	flag.Parse()

	sip.UDPMTUSize = 10000
	if *pprof {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
		runtime.MemProfileRate = 64
	}

	lev := slog.LevelInfo
	debuglev := os.Getenv("LOGDEBUG")
	if *debflag || debuglev != "" {
		lev = slog.LevelDebug
		sip.SIPDebug = true
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lev})))

	slog.Info("Runtime", "cpus", runtime.NumCPU())
	slog.Info("Server routes setuped")
	go httpServer(":8080")

	srv := setupSipProxy(*dst, *extIP)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := srv.ListenAndServe(ctx, *transportType, *extIP); err != nil {
		slog.Error("Fail to start sip server", "error", err)
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
	statsviz.Register(http.DefaultServeMux)

	slog.Info("Http server started address=%s", address)
	http.ListenAndServe(address, nil)
}

func setupSipProxy(proxydst string, ip string) *sipgo.Server {
	// Prepare all variables we need for our service
	host, port, _ := sip.ParseAddr(ip)
	ua, err := sipgo.NewUA()
	if err != nil {
		slog.Error("Fail to setup user agent", "error", err)
		os.Exit(1)
	}

	srv, err := sipgo.NewServer(ua)
	if err != nil {
		slog.Error("Fail to setup server handle", "error", err)
		os.Exit(1)
	}

	client, err := sipgo.NewClient(ua, sipgo.WithClientAddr(
		ip,
	))
	if err != nil {
		slog.Error("Fail to setup client handle", "error", err)
		os.Exit(1)
	}

	registry := NewRegistry()
	var getDestination = func(req *sip.Request) string {
		tohead := req.To()
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
			slog.Error("Fail to respond on transaction", "error", err)
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

		ctx := context.Background()

		req.SetDestination(dst)
		// Start client transaction and relay our request
		clTx, err := client.TransactionRequest(ctx, req, sipgo.ClientRequestAddVia, sipgo.ClientRequestAddRecordRoute)
		if err != nil {
			slog.Error("RequestWithContext  failed", "error", err)
			reply(tx, req, 500, "")
			return
		}
		defer clTx.Terminate()

		// Keep monitoring transactions, and proxy client responses to server transaction
		slog.Debug("Starting transaction", "req", req.Method.String())
		for {
			select {

			case res, more := <-clTx.Responses():
				if !more {
					return
				}

				res.SetDestination(req.Source())

				// https://datatracker.ietf.org/doc/html/rfc3261#section-16.7
				// Based on section removing via. Topmost via should be removed and check that exist

				// Removes top most header
				res.RemoveHeader("Via")
				if err := tx.Respond(res); err != nil {
					slog.Error("ResponseHandler transaction respond failed", "error", err)
				}

			// Early terminate
			// if req.Method == sip.BYE {
			// 	// We will call client Terminate
			// 	return
			// }
			case <-clTx.Done():
				if err := tx.Err(); err != nil {
					slog.Error("Client Transaction done with error", "error", err, "req", req.Method.String())
				}
				return

			case m := <-tx.Acks():
				// Acks can not be send directly trough destination
				slog.Info("Proxing ACK", "m", m.StartLine(), "dst", dst)
				m.SetDestination(dst)
				client.WriteRequest(m)

			case <-tx.Done():
				if err := tx.Err(); err != nil {
					if errors.Is(err, sip.ErrTransactionCanceled) {
						// Cancel other side. This is only on INVITE needed
						// We need now new transaction
						if req.IsInvite() {
							r := newCancelRequest(req)
							res, err := client.Do(ctx, r)
							if err != nil {
								slog.Error("Canceling transaction failed", "err", err, "req", req.Method.String())
								return
							}
							if res.StatusCode != 200 {
								slog.Error("Canceling transaction failed with non 200 code", "err", err, "req", req.Method.String())
								return
							}
							return
						}
					}

					slog.Error("Transaction done with error", "err", err, "req", req.Method.String())
					return
				}
				slog.Debug("Transaction done", "req", req.Method.String())
				return
			}
		}
	}

	var registerHandler = func(req *sip.Request, tx sip.ServerTransaction) {
		// https://www.rfc-editor.org/rfc/rfc3261#section-10.3
		cont := req.Contact()
		if cont == nil {
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
		slog.Debug("Contact added %s -> %s", "src", uri.User, "dst", addr)

		res := sip.NewResponseFromRequest(req, 200, "OK", nil)
		// slog.Debug().Msgf("Sending response: \n%s", res.String())

		// URI params must be reset or this should be regenetad
		cont.Address.UriParams = sip.NewParams()
		cont.Address.UriParams.Add("transport", req.Transport())

		if err := tx.Respond(res); err != nil {
			slog.Error("Sending REGISTER OK failed", "error", err)
			return
		}
	}

	var inviteHandler = func(req *sip.Request, tx sip.ServerTransaction) {
		route(req, tx)
	}

	var ackHandler = func(req *sip.Request, tx sip.ServerTransaction) {
		dst := getDestination(req)
		if dst == "" {
			return
		}
		req.SetDestination(dst)
		if err := client.WriteRequest(req, sipgo.ClientRequestAddVia); err != nil {
			slog.Error("Send failed", "error", err)
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

func newCancelRequest(inviteRequest *sip.Request) *sip.Request {
	cancelReq := sip.NewRequest(sip.CANCEL, inviteRequest.Recipient)
	cancelReq.AppendHeader(sip.HeaderClone(inviteRequest.Via())) // Cancel request must match invite TOP via and only have that Via
	cancelReq.AppendHeader(sip.HeaderClone(inviteRequest.From()))
	cancelReq.AppendHeader(sip.HeaderClone(inviteRequest.To()))
	cancelReq.AppendHeader(sip.HeaderClone(inviteRequest.CallID()))
	sip.CopyHeaders("Route", inviteRequest, cancelReq)
	cancelReq.SetSource(inviteRequest.Source())
	cancelReq.SetDestination(inviteRequest.Destination())
	return cancelReq
}

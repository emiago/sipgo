# SIPGO

[![Go Report Card](https://goreportcard.com/badge/github.com/emiago/sipgo)](https://goreportcard.com/report/github.com/emiago/sipgo) 
[![License](https://img.shields.io/badge/License-BSD_2--Clause-orange.svg)](https://github.com/emiago/sipgo/LICENCE) 
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/emiago/sipgo)

Library for writing fast SIP servers in GO language.  
It comes with SIP stack ([RFC 3261](https://datatracker.ietf.org/doc/html/rfc3261)) optimized for fast parsing.


Fetch lib with:

`go get github.com/emiago/sipgo`

**NOTE**: LIB MAY HAVE API CHANGES UNTIL STABLE VERSION.

## Performance

As example you can find `example/proxysip` as simple version of statefull proxy. It is used for stress testing with `sipp`. 
To find out more about performance check the latest results:  
[example/proxysip](example/proxysip) 

(Contributions are welcome, I would place your results here)

## Examples

- Stateful proxy [example/proxysip](example/proxysip)  
- Register with authentication [example/register](example/register)  
- Dialog [example/dialog](example/dialog)  

***If you use this lib in some way, open issue for more sharing.***
## Usage

Lib allows you to write easily sip servers(or clients) or to build up stateful proxies, registrar or any sip routing.
Writing in GO we are not limited to handle SIP requests/responses in many ways, or to integrate and scale with any external services (databases, caches...).


### UAS/UAC build

Using server or client handle for UA you can build incoming or outgoing requests.

```go
ua, _ := sipgo.NewUA() // Build user agent
srv, _ := sipgo.NewServer(ua) // Creating server handle
client, _ := sipgo.NewClient(ua) // Creating client handle
srv.OnInvite(inviteHandler)
srv.OnAck(ackHandler)
srv.OnCancel(cancelHandler)
srv.OnBye(byeHandler)

// For registrars
// srv.OnRegister(registerHandler)
ctx, _ := signal.NotifyContext(ctx, os.Interrupt)
go srv.ListenAndServe(ctx, "udp", "127.0.0.1:5060")
go srv.ListenAndServe(ctx, "tcp", "127.0.0.1:5061")
go srv.ListenAndServe(ctx, "ws", "127.0.0.1:5080")
<-ctx.Done()
```

- Server handle creates listeners and reacts on incoming requests. [More on server transactions](#server-transaction)
- Client handle allows creating transaction requests [More on client transactions](#client-transaction)


#### TLS transports
```go 
// TLS
conf :=  sipgo.GenerateTLSConfig(certFile, keyFile, rootPems)
srv.ListenAndServeTLS(ctx, "tcp", "127.0.0.1:5061", conf)
srv.ListenAndServeTLS(ctx, "ws", "127.0.0.1:5081", conf)
```

## Stateful Proxy build

Proxy is combination client and server handle that creates server/client transaction. They need to share
same ua same like uac/uas build.
Forwarding request is done via client handle:
```go

srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
    req.SetDestination("10.1.2.3") // Change sip.Request destination
    // Start client transaction and relay our request. Add Via and Record-Route header
    clTx, err := client.TransactionRequest(req, sipgo.ClientRequestAddVia, sipgo.ClientRequestAddRecordRoute)
    // Send back response
    res := <-cltx.Responses()
    tx.Respond(res)
})
```

Checkout `/example/proxysip` for more how to build simple stateful proxy.


### Server Transaction

Server transaction is passed on handler

```go
// Incoming request
srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
    res := sip.NewResponseFromRequest(req, code, reason, body)
    // Send response
    tx.Respond(res)

    select {
        case m := <-tx.Acks(): // Handle ACK . ACKs on 2xx are send as different request
        case m := <-tx.Cancels(): // Handle Cancel 
        case <-tx.Done():
            // Signal transaction is done. 
            // Check any errors with tx.Err() to have more info why terminated
            return
    }

    // terminating handler terminates Server transaction automaticaly
})

```

### Server stateless response

```go
srv := sipgo.NewServer()
...
func ackHandler(req *sip.Request, tx sip.ServerTransaction) {
    res := sip.NewResponseFromRequest(req, code, reason, body)
    srv.WriteResponse(res)
}
srv.OnACK(ackHandler)
```


### Client Transaction

Using client handle allows easy creating and sending request. All you need is this.
```go
req := sip.NewRequest(sip.INVITE, recipient)
tx, err := client.TransactionRequest(req, opts...) // Send request and get client transaction handle
```
Unless you customize transaction request with opts by default `client.TransactionRequest` will build all other
headers needed to pass correct sip request.

Here is full example:
```go
client, _ := sipgo.NewClient(ua) // Creating client handle

// Request is either from server request handler or created
req.SetDestination("10.1.2.3") // Change sip.Request destination
tx, err := client.TransactionRequest(req) // Send request and get client transaction handle

defer tx.Terminate() // Client Transaction must be terminated for cleanup
...

select {
    case res := <-tx.Responses():
    // Handle responses
    case <-tx.Done():
    // Wait for termination
    return
}

```
NOTE: If you are building UA that also has server handle on UDP. UDP listener will be reused to also send packets.

### Client stateless request

```go
client, _ := sipgo.NewClient(ua) // Creating client handle
req := sip.NewRequest(method, &recipment)
// Send request and forget
client.WriteRequest(req)
```

### Dialogs (experiment)

NOTE: This may be redesigned to have more control

`ServerDialog` is extended type of Server with Dialog support. 
For now this is in experiment.

```go
srv, err := sipgo.NewServerDialog(ua)
...
srv.OnDialog(func(d sip.Dialog) {
    switch d.State {
	case sip.DialogStateEstablished:
		// 200 response
	case sip.DialogStateConfirmed:
		// ACK send
	case sip.DialogStateEnded:
		// BYE send
	}
})

```

`ClientDialog` TODO...

## SIP Debug

You can have full SIP messages dumped from transport into Debug level message.

Example:
```go
transport.SIPDebug = true
```

```
Feb 24 23:32:26.493191 DBG UDP read 10.10.0.10:5060 <- 10.10.0.100:5060:
SIP/2.0 100 Trying
Via: SIP/2.0/UDP 10.10.0.10:5060;rport=5060;received=10.10.0.10;branch=z9hG4bK.G3nCwpXAKJQ0T2oZUII70wuQx9NeXc61;alias
Via: SIP/2.0/UDP 10.10.1.1:5060;branch=z9hG4bK-1-1-0
Record-Route: <sip:10.10.0.10;transport=udp;lr>
Call-ID: 1-1@10.10.1.1
From: "sipp" <sip:sipp@10.10.1.1>;tag=1SIPpTag001
To: "uac" <sip:uac@10.10.0.10>
CSeq: 1 INVITE
Server: Asterisk PBX 18.16.0
Content-Length:  0
```


## Documentation
More on documentation you can find on [Go doc](https://pkg.go.dev/github.com/emiago/sipgo)


## Supported protocols

- [x] UDP
- [x] TCP
- [x] TLS
- [x] WS
- [x] WSS


## Tests

Library will be covered with more tests. Focus is more on benchmarking currently.
```
go test ./...  
```

## Credits

This project was based on [gosip](https://github.com/ghettovoice/gosip) by project by @ghetovoice, but started as new project to achieve best/better performance and to improve API.
This unfortunately required many design changes, therefore this libraries are not compatible.

## Support

If you find this project interesting for bigger support or contributing, you can contact me on
[mail](emirfreelance91@gmail.com) 

For bugs features pls create [issue](https://github.com/emiago/sipgo/issues).



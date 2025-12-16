<img src="icons/icon.png" width="300" alt="SIPGO">

[![Go Report Card](https://goreportcard.com/badge/github.com/emiago/sipgo)](https://goreportcard.com/report/github.com/emiago/sipgo)
![Used By](https://sourcegraph.com/github.com/emiago/sipgo/-/badge.svg)
![Coverage](https://img.shields.io/badge/coverage-55.4%25-blue)
[![License](https://img.shields.io/badge/License-BSD_2--Clause-orange.svg)](https://github.com/emiago/sipgo/LICENCE) 
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/emiago/sipgo)

**SIPGO** is library for writing fast SIP services in GO language.  
It comes with [SIP stack](/sip/README.md) ([RFC 3261](https://datatracker.ietf.org/doc/html/rfc3261)|[RFC3581](https://datatracker.ietf.org/doc/html/rfc3581)|[RFC6026](https://datatracker.ietf.org/doc/html/rfc6026)) optimized for fast parsing.


**Libs on top of sipgo:**
- ***diago*** [github.com/emiago/diago](https://github.com/emiago/diago): Full VOIP library/framework with media stack 

**Tools/Service:**
- <img width="20" src="https://github.com/emiago/diagox/raw/main/images/diagox-icon-blue.png"> [github.com/emiago/diagox](https://github.com/emiago/diagox) simple Ingress/Egress and Registrar for SIP/RTP scaling
- <img width="20" src="https://github.com/emiago/gophone/raw/main/images/g2.png"> [github.com/emiago/gophone](https://github.com/emiago/gophone) CLI softphone for easy testing 


Fetch lib with:

`go get github.com/emiago/sipgo`


If you like/use project currently and looking for support/sponsoring  checkout [Support section](#support)  

You can follow on [X/Twitter](https://twitter.com/emiago123) for more updates.

More on documentation you can find on [Go doc](https://pkg.go.dev/github.com/emiago/sipgo)

## Supported protocols

- [x] UDP
- [x] TCP
- [x] TLS
- [x] WS
- [x] WSS


### RFC:
- [RFC3261](https://datatracker.ietf.org/doc/html/rfc3261)
- [RFC3263](https://datatracker.ietf.org/doc/html/rfc3263)
- [RFC3581](https://datatracker.ietf.org/doc/html/rfc3581)
- [RFC6026](https://datatracker.ietf.org/doc/html/rfc6026)

State of Torture Tests [RFC4475](https://datatracker.ietf.org/doc/html/rfc6026) you can find on issue [github.com/emiago/sipgo/issues/57](https://github.com/emiago/sipgo/issues/57)
but NOTE: some strict validation things may 
be seperated from parsing or not built into library.


## Examples

- Stateful proxy [example/proxysip](example/proxysip)  
- Register with authentication [example/register](example/register)  
- RTP echo with sipgox [example/dialog](https://github.com/emiago/sipgox/tree/main/echome)

Also thanks to [pion](https://github.com/pion/webrtc) project sharing this example of using SIPgo with webrtc:
- https://github.com/pion/example-webrtc-applications/tree/master/sip-to-webrtc  original post [on X](https://twitter.com/_pion/status/1742955942314913958)




## Performance :rocket

SIPgo was proven that can excel in performance compared to some other configured base proxy solutions. 

As an example, you can find `example/proxysip` as simple version of statefull proxy. It is used for stress testing with `sipp`. 
To find out more about performance check the latest results:  
[example/proxysip](example/proxysip) 



# Usage

Lib allows you to write easily sip servers, clients, stateful proxies, registrars or any sip routing.
Writing in GO we are not limited to handle SIP requests/responses in many ways, or to integrate and scale with any external services (databases, caches...).


## UAS/UAC build

Using server or client handle for UA you can build incoming or outgoing requests.

```go
ua, _ := sipgo.NewUA() // Build user agent
srv, _ := sipgo.NewServer(ua) // Creating server handle for ua
client, _ := sipgo.NewClient(ua) // Creating client handle for ua
srv.OnInvite(inviteHandler)
srv.OnAck(ackHandler)
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


### TLS transports
```go 
// TLS
conf := generateTLSConfig(certFile, keyFile, rootPems)
srv.ListenAndServeTLS(ctx, "tcp", "127.0.0.1:5061", conf)
srv.ListenAndServeTLS(ctx, "ws", "127.0.0.1:5081", conf)

func generateTLSConfig(certFile string, keyFile string, rootPems []byte) (*tls.Config, error) {
	roots := x509.NewCertPool()
	if rootPems != nil {
		ok := roots.AppendCertsFromPEM(rootPems)
		if !ok {
			return nil, fmt.Errorf("failed to parse root certificate")
		}
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("fail to load cert. err=%w", err)
	}

	conf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      roots,
	}

	return conf, nil
}
```

### UAC first

If you are acting as client first, you can say to client which host:port to use, and this connection will be
reused until closing UA. Any request received can be still processed with server handle.

```go
ua, _ := sipgo.NewUA() // Build user agent
defer ua.Close()

client, _ := sipgo.NewClient(ua, sipgo.WithClientHostname("127.0.0.1"), sipgo.WithClientPort(5060))
server, _ := sipgo.NewServer(ua) 
srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction)) {
    // This will be received on 127.0.0.1:5060
}

tx, err := client.TransactionRequest(ctx, sip.NewRequest(sip.INVITE, recipient)) 
```



## Server Transaction

Server transaction is passed on handler

```go
// Incoming request
srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
    // Send provisional
    res := sip.NewResponseFromRequest(req, 100, "Trying", nil)
    tx.Respond(res)
    
    // Send OK. TODO: some body like SDP
    res := sip.NewResponseFromRequest(req, 200, "OK", body)
    tx.Respond(res)

    // Wait transaction termination.
    select {
        case m := <-tx.Acks(): // Handle ACK for response . ACKs on 2xx are send as different request
        case <-tx.Done():
            // Signal transaction is done. 
            // Check any errors with tx.Err() to have more info why terminated
            return
    }
    // terminating handler terminates Server transaction automaticaly
})

```

## Server stateless response

```go
srv := sipgo.NewServer()
...
func ackHandler(req *sip.Request, tx sip.ServerTransaction) {
    res := sip.NewResponseFromRequest(req, code, reason, body)
    srv.WriteResponse(res)
}
srv.OnACK(ackHandler)
```

## Client Do request

Unless you need more control over [Client Transaction](#client-transaction) you can simply go with client `Do` request and wait final response (following std http package).

```go
req := sip.NewRequest(sip.INVITE, sip.Uri{User:"bob", Host: "example.com"})
res, err := client.Do(req)
```

## Client Transaction

Using client handle allows easy creating and sending request. 
Unless you customize transaction request with opts by default `client.TransactionRequest` will build all other
headers needed to pass correct sip request.

Here is full example:
```go
ctx := context.Background()
client, _ := sipgo.NewClient(ua) // Creating client handle

req := sip.NewRequest(sip.INVITE, sip.Uri{User:"bob", Host: "example.com"})
tx, err := client.TransactionRequest(ctx, req) // Send request and get client transaction handle

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

#### CSEQ Header increase rule: 
1. Every new transaction will have **implicitely** CSEQ increase if present -> [Issue 160](https://github.com/emiago/sipgo/issues/160). This fixes problem when you are passing same request like ex. REGISTER
2. Above rule does not apply for In Dialog cases
2. To avoid 1. pass option `client.TransactionRequest(ctx, req, sipgo.ClientRequestBuild)` or more better used `DialogClient`

## Client stateless request

```go
client, _ := sipgo.NewClient(ua) // Creating client handle
req := sip.NewRequest(sip.ACK, sip.Uri{User:"bob", Host: "example.com"})
// Send request and forget
client.WriteRequest(req)
```

## Dialog handling

`DialogUA` is helper struct to create `Dialog`. 
**Dialog** can be **as server** or **as client** created. Later on this provides you RFC way of sending request within dialog `Do` or `TransactionRequest` functions.

For basic usage `DialogClientCache` and `DialogServerCache` are created to be part of library to manage and cache dialog accross multiple request.
They are seperated based on your **request context**, but they act more like `peer`.

---
**NOTE**: **It is recomended that you build your OWN Dialog Server/Client Cache mechanism for dialogs.**

---

For basic control some handling request wrappers like `Ack`, `Bye`, `ReadAck`, `ReadBye` is provided. Sending  any other request should be done with `Do` or receiving can be validated with `ReadRequest`


**UAC**:
```go
ua, _ := sipgo.NewUA() // Build user agent
srv, _ := sipgo.NewServer(ua) // Creating server handle
client, _ := sipgo.NewClient(ua) // Creating client handle

contactHDR := sip.ContactHeader{
    Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5088},
}
dialogCli := sipgo.NewDialogClientCache(client, contactHDR)

// Attach Bye handling for dialog
srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
    err := dialogCli.ReadBye(req, tx)
    //handle error
})

// Create dialog session
dialog, err := dialogCli.Invite(ctx, recipientURI, nil)
defer dialog.Close() // Cleans up from dialog pool
// Wait for answer
err = dialog.WaitAnswer(ctx, AnswerOptions{})
// Check dialog response dialog.InviteResponse (SDP) and return ACK
err = dialog.Ack(ctx)
// Send BYE to terminate call
err = dialog.Bye(ctx)
```

**UAS**:
```go
ua, _ := sipgo.NewUA() // Build user agent
srv, _ := sipgo.NewServer(ua) // Creating server handle
client, _ := sipgo.NewClient(ua) // Creating client handle

uasContact := sip.ContactHeader{
    Address: sip.Uri{User: "test", Host: "127.0.0.200", Port: 5099},
}
dialogSrv := sipgo.NewDialogServerCache(client, uasContact)

srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
    dlg, err := dialogSrv.ReadInvite(req, tx)
    if err != nil {
        return err
    }
    defer dlg.Close() // Close for cleanup from cache
    // handle error
    dlg.Respond(sip.StatusTrying, "Trying", nil)
    dlg.Respond(sip.StatusOK, "OK", nil)
    
    // Instead Done also dlg.State() can be used for granular state checking
    <-dlg.Context().Done()
})

srv.OnAck(func(req *sip.Request, tx sip.ServerTransaction) {
    dialogSrv.ReadAck(req, tx)
})

srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
    dialogSrv.ReadBye(req, tx)
})
```

### Dialog Do/Transaction request

For any other request within dialog you should use `dialog.Do` to send request. Requests withing dialog need more 
checks to pass and therefore this API helps to keep requests within dialog session.
```go
req := sip.NewRequest(sip.INFO, recipient)
res, err := dialog.Do(ctx, req)
```


## Stateful Proxy build

Proxy is combination client and server handle that creates server/client transaction. They need to share
same **ua** same like uac/uas build.

Forwarding request is done via client handle:
```go
ua, _ := sipgo.NewUA() // Build user agent
srv, _ := sipgo.NewServer(ua) // Creating server handle
client, _ := sipgo.NewClient(ua) // Creating client handle

srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
    ctx := context.Background()
    req.SetDestination("10.1.2.3") // Change sip.Request destination
    // Start client transaction and relay our request. Add Via and Record-Route header
    clTx, err := client.TransactionRequest(ctx, req, sipgo.ClientRequestAddVia, sipgo.ClientRequestAddRecordRoute)
    // Send back response
    res := <-cltx.Responses()
    tx.Respond(res)
})
```

## SIP Debug

You can have full SIP messages dumped from transport into Debug level message.

Example:
```go
sip.SIPDebug = true
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

## Support

If you find this project interesting for bigger support or consulting, you can contact me on
[mail](mailto:emirfreelance91@gmail.com)

For bugs features pls create [issue](https://github.com/emiago/sipgo/issues).

## Extra


### E2E/integration testing

If you are interested using lib for your testing services then checkout 
[article on how easy you can make calls and other](https://github.com/emiago/sipgo/wiki/E2E-testing)


### Tests

Library will be covered with more tests. Focus is more on benchmarking currently.
```
go test ./...  
```

## Credits

This project was influenced by [gosip](https://github.com/ghettovoice/gosip), project by @ghetovoice, but started as new project to achieve best/better performance and to improve API.
This unfortunately required many design changes, therefore this libraries are not compatible.


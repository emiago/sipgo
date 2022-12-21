# SIPGO

Library for writing fast SIP servers in GO language.  
It comes with SIP stack ([RFC 3261](https://datatracker.ietf.org/doc/html/rfc3261)) optimized for fast parsing.

**NOTE**: LIB IS IN DEV. API CAN CHANGE


## Performance

As example you can find `example/proxysip` as simple version of statefull proxy. It is used for stress testing with `sipp`. 
To find out more about performance check the latest results:  
[example/proxysip](example/proxysip) 

(Contributions are welcome, I would place your results here)

## Examples

Stateful proxy [example/proxysip](example/proxysip)  
Dialog [example/dialog](example/dialog)  

## Usage

Lib allows you to write easily client or server or to build up stateful proxies, registrar or any sip routing.
Writing in GO we are not limited to handle SIP requests/responses in many ways, or to integrate and scale with any external services (databases, caches...).


### UAS/UAC build

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


// Add listeners
srv.Listen("udp", "127.0.0.1:5060")
srv.Listen("tcp", "127.0.0.1:5061")
srv.Listen("ws", "127.0.0.1:5080")
...
// fire server
srv.Serve()
```

 

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

**NOTE**: UA needs server handle and listener on same network before sending request


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

### Client stateless request

```go
client, _ := sipgo.NewClient(ua) // Creating client handle
req := sip.NewRequest(method, &recipment, "SIP/2.0")
// Send request and forget
client.WriteRequest(req)
```

## Proxy build

Proxy is combination client and server handle. 
Checkout `/example/proxysip` for more how to build simple stateful proxy.


### Dialogs (experiment)

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

## Documentation
More on documentation you can find on [Go doc](https://pkg.go.dev/github.com/emiago/sipgo)


## Supported protocols

- [x] UDP
- [x] TCP
- [ ] TLS
- [x] WS
- [ ] WSS


## Tests

Library will be covered with more tests. Focus is more on benchmarking currently.
```
go test ./...  
```

## Credits

This project was based on [gosip](https://github.com/ghettovoice/gosip) by project by @ghetovoice, but started as new project to achieve best/better performance and to improve API.
This unfortunately required many design changes, therefore this libraries are not compatible.

## Support

If you find this project interesting for support or contributing, you can contact me on
[mail](emirfreelance91@gmail.com) 

For bugs features pls create [issue](https://github.com/emiago/sipgo/issues).



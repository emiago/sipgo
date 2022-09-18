# sipgo

Library for writing fast SIP servers in GO language.  
It comes with SIP stack ([RFC 3261](https://datatracker.ietf.org/doc/html/rfc3261)) optimized for fast parsing.

### NOTE: PROJECT IS IN DEV, API CAN CHANGE
## Examples

As example you can find `example/proxysip` as simple version of statefull proxy. It is used for stress testing with `sipp`. 
To find out more about performance check the latest results:  
[example/proxysip](example/proxysip) 

(Contributions are welcome, I would place your results here)
## Usage

Lib allows you to write easily stateful proxies, registrar or any sip routing.
Writing in GO we are not limited to handle SIP requests/responses in many ways, or to integrate and scale with any external services (databases, caches...).

```go
srv := sipgo.NewServer()
srv.OnRegister(registerHandler)
srv.OnInvite(inviteHandler)
srv.OnAck(ackHandler)
srv.OnCancel(cancelHandler)
srv.OnBye(byeHandler)

// Add listeners
srv.Listen("udp", "127.0.0.1:5060")
srv.Listen("tcp", "127.0.0.1:5061")
...
// Start serving
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

### Client Transaction

```go

// Request is either from server request handler or created
req.SetDestination("10.1.2.3") // Change sip.Request destination
tx, err := srv.TransactionRequest(req) // Send request and get client transaction handle

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

### Stateless response

```go
srv := sipgo.NewServer()
...
func ackHandler(req *sip.Request, tx sip.ServerTransaction) {
    res := sip.NewResponseFromRequest(req, code, reason, body)
    srv.WriteResponse(res)
}
srv.OnACK(ackHandler)
```

### Dialogs

TODO...
- Monitoring
- Dialog control

## Documentation
More on documentation you can find on [Go doc](https://pkg.go.dev/github.com/emiraganov/sipgo)


## Supported protocols

- [x] UDP
- [x] TCP
- [ ] TLS
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

For bugs features pls create [issue](https://github.com/emiraganov/sipgo/issues).

Development:
- Always benchmark and keep high performance. 
- Add most used transport protocols
- Stabilize API and more tests


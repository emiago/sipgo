# sipgo

Library for writing fast SIP servers in GO language.  
It comes with SIP stack ([RFC 3261](https://datatracker.ietf.org/doc/html/rfc3261)) optimized for fast parsing.

This project was based on [gosip](https://github.com/ghettovoice/gosip) by project by @ghetovoice, but started as new project to achieve best/better performance and to improve API.
This unfortunately required many design changes, therefore this libraries are not compatible.

## Examples

As example you can find `cmd/proxysip` as simple version of statefull proxy. It is used for stress testing with `sipp`. 
To find out more about performance check the latest results:  
[cmd/proxysip](cmd/proxysip) 
## Usage (API not stable)

Lib allows you to write easily stateful proxies, registrar or any sip routing.
Writing in GO we are not limited to handle SIP requests/responses in many ways, or to integrate and scale with any external services (databases, caches...).

```
srv := sipgo.NewServer()
srv.OnRegister(registerHandler)
srv.OnInvite(inviteHandler)
srv.OnAck(ackHandler)
srv.OnCancel(cancelHandler)
srv.OnBye(byeHandler)
```

**TODO** more docs  
...

### NOTE: PROJECT IS EXPERIMENTAL, API CAN CHANGE



## Supported protocols

- [x] UDP
- [ ] TCP
- [ ] TLS
- [ ] WSS


# SIP stack in GO

This SIP stack for RFC: 

https://datatracker.ietf.org/doc/html/rfc3261


Stack:
- Encoding/Decoding with `Parser` optimized for fast parsing
- Transport Layer and support for different protocols
- Transaction Layer with transaction sessions managing and state machine


## Parser

Parser by default parses set of headers that are mostly present in messages. From,To,Via,Cseq,Content-Type,Content-Length...
This headers are accessible via fast reference `msg.Via()`, `msg.From()`...

This can be configured using `WithHeadersParsers` and reducing this to increase performance. 
SIP stack in case needed will use fast reference and lazy parsing.
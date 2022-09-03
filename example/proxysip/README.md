# proxysip

Is simple stateful proxy for handling SIP calls.

## Usage
```
proxysip:
  -debug
    	
  -dst string
    	Destination pbx, sip server (default "127.0.0.2:5060")
  -ip string
    	My exernal ip (default "127.0.0.1:5060")
  -pprof
    	Full profile
  -t string
    	Transport, default will be determined by request (default "udp")
```


# Stress testing UDP with sipp (sipgo and opensips)

*SIPGO can handle a lot of calls in sec, and lot of performance improvements are done.*  
We are comparing with `opensips` (C based) so you can find similar configuration for opensips, handling simple proxy behavior.

**NOTE**: *Consider this test results are not `100%` accurate. They probably need better setup, but for now they are added for some overview.*

This tests are done on local machine using docker, so they should be easily rerun on different env.
Due to local resources limitation we limit our proxy to 4 CPU cores.

In stress testing we are looking:
  - response time (<200ms)
  - failed calls

## Setup

`docker-compose` is used for quickly running proxy and sipp. You check more in docker-compose.yml about configuration.
`sipp` are used standard `uac` and `uas` scenarios. 

This can be simply rerun with docker-compose:

```
# Run this in 3 terminals
docker-compose run proxy
docker-compose run uas
docker-compose run uac
```

## Results
We pushed that rate of calls until proxy starts constantly faling calls.
For some i7 2.60 GHZ (limited to 4 core) it can handle 
- *more than 2000 calls/s rate*
- *peak more than 12000 calls*

## Tradeoffs
Library will cache a lot to remove GO GC pressure, and therefore you should expect
`HIGH` memory usage. 
Performance can be improved with increasing GOGC (ex GOGC=200) to remove GC pressure,
but higher memory usage shoud be expected.

**CONSIDER** checking latest GO version with support of limiting memory usage


### NOTE

If you are using containers, GOMAXPROCS is important to much cpu quota. Alternative use ubers automaxprocs
```
import _ "go.uber.org/automaxprocs"
```
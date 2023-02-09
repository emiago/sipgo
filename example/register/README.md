# register

This example provides example of running simple registry and client with Digest auth.
It has some external dependency so it is a submodule.
To run in from root use workspace:
```sh
go work init 
go work use .
go work use ./example/register
```


## Running server:

```sh 
go run ./server -u "alice:alice,bob:bob" -ip 127.0.0.10:5060
```
Configure list of username:password with `-u` parameter.

## Running client:

```sh
go run ./client -u alice -p alice -srv 127.0.0.10:5060
go run ./client -u bob -p bob -srv 127.0.0.10:5060
```
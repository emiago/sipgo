# register

This example provides example of running simple registry and client with Digest auth.

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
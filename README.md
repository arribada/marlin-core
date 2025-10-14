# marlin-core
Code for the core box of the marlin project

# requirements

To build/deploy the code you'll need Docker (as everything is meant to be built from within docker), and Go. You may be able to do away without Go if you run things directly from the Makefile though.

# how to

The top-level entry point is `make.go`. e.g. to deploy the code to the marlin-augnat2 instance/vessel:
```
go run ./make.go -vessel marlin-augnat2
```

Using make.go makes sure that the $instance var is set, and it injects on the fly some variables and secrets into the instance's gokrazy config file.

Next level is the ```Makefile```, which is used by make.go.

A top level target in the Makefile (e.g. ```make update```) triggers a docker build, so the next step to understand what's actually happening is to follow along in the ```Dockerfile```.

Finally, from within docker, we build all the different services and generate a gokrazy image (.gaf). Some of the steps actually call back into the Makefile (e.g. ```make gaf2```).

The very last step of an update (calling ```./bin/ingest```), is done out of docker because it involves communicating with the ```GUS server``` (notably pushing the gokrazy image to it), which we want to do over tailscale (just like any communication with any other part of the fleet/system). So we'd have to be able to connect to our tailscale network from within docker, which is annoying.

# GUS server

Get/build it from ```https://github.com/gokrazy/gus```, then run it on a machine within your tailscale network.
See ```./bin/gus_launcher.sh``` as an example.

# initial provisioning

See https://github.com/arribada/marlin-core/blob/main/docs/init.txt


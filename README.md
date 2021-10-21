![terrarium](doc/terrarium-with-text.png)

[![Build
Status](https://travis-ci.org/eyedeekay/terrarium.svg)](https://travis-ci.org/eyedeekay/terrarium)
[![Go Report
Card](https://goreportcard.com/badge/i2pgit.org/idk/terrarium)](https://goreportcard.com/report/i2pgit.org/idk/terrarium)

terrarium is an IRC server with a focus on being small and understandable,
originally forked from [horgh/catbox](https://github.com/horgh/catbox). The
goal is to create an easy-to-configure I2P IRC server which is highly stable
and secure, while retaining the ability to link with non-I2P IRC servers using
TLS in order to bridge anonymous and non-anonymous chat. For now, Bridged
servers are not anonymous, this may change in the future as I evaluate the
feasibility of outproxies or Tor.


# Features
* Server to server linking
* IRC operators
* Private (WHOIS shows no channels, LIST isn't supported)
* Flood protection
* K: line style connection banning
* TLS

terrarium implements enough of [RFC 1459](https://tools.ietf.org/html/rfc1459)
to be recognisable as IRC and be minimally functional. It will intentionally
omit unnecessary features. Priority features are those which enable moderation
and provide more flexible security.


# Installation
1. Clone the software from [i2pgit.org](https://i2pgit.org/idk/terrarium)
   (`git clone https://i2pgit.org/idk/terrarium go/src/i2pgit.org/idk/terrarium && cd go/src/i2pgit.org/idk/terrarium`).
2. Build from source
   (`go build`).
3. Configure terrarium through config files. There are example configs in the
   `conf` directory. All settings are optional and have defaults.
4. Run it, e.g. `./terrarium -conf terrarium.conf`. You might run it via systemd
   via a service such as:

```
[Service]
ExecStart=/home/ircd/terrarium/terrarium -conf /home/ircd/terrarium/terrarium.conf
Restart=always

[Install]
WantedBy=default.target
```


# Configuration

## terrarium.conf
Global server settings.


## opers.conf
IRC operators.


## servers.conf
The servers to link with.


## users.conf
Privileges and hostname spoofs for users.

The only privilege right now is flood exemption.


## TLS
A setup for a network might look like this:

* Give each server a certificate with 2 SANs: Its own hostname, e.g.
  server1.example.com, and the network hostname, e.g. irc.example.com.
* Set up irc.example.com with DNS round-robin listing each server's IP.
* List each server by its own hostname in servers.conf.

Clients connect to the network hostname and verify against it. Servers
connect to each other by server hostname and verify against it.


## I2P
An example I2P configuration can be found in:

`conf/catbox-i2p.conf`

That's all the docs I have for now

# Why the name?
It was forked from an IRC server called catbox which had a focus on simplicity
and understandability. It now has the ability to connect to other IRC servers
through I2P Tunnels. Clearnet is to I2P Tunnels is sort of like Catbox is to
Terrarium.


# Logo
terrarium logo (c) 2017 Bee

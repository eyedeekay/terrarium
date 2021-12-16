# 0.0.08

- include license in plugin package
- fix license in plugin config

# 0.0.07

- restart changelog.
- fix websiteURL in plugin.

# 1.14.0

* Stop publishing arm releases.
* Support TLS 1.3.


# 1.13.0 (2019-07-08)

* Include Go version in version string.


# 1.12.0 (2019-07-06)

* Update dependencies.
* Send messages during connect immediately rather than only after we've
  performed our reverse DNS lookup.
* Stop logging client reads/writes.


# 1.11.0 (2019-01-01)

* No longer automatically rehash once a week. I changed my mind about this!


# 1.10.0 (2018-08-18)

* Rehashing now reloads the TLS certificate and key.
* Rehashing now automatically happens once a week. This is so we load new
  certificates.
* Require valid TLS certificates on outbound TLS connections. This means
  servers we connect to must have valid certificates that match the name we
  connect to them as.


# 1.9.0 (2018-07-28)

* Started tracking changes in a changelog.
* If a message is invalid, send a notice to opers about it rather than just
  log. This is to catch bugs arising from this behaviour.
* Send a notice to opers if there is an unprocessed buffer after a read
  error. This is again to catch bugs from this behaviour.
* Failing to set read deadlines now logs rather than triggers client quit.
  This is to allow for the read to happen which can have data in the
  buffer.
* Delay before flagging the client as dead if there is a write error. This
  is to give us a window to read anything further from the client that
  might be available.

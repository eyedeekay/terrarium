This is yet another IRC server! I'm creating it because I enjoy working with
IRC and I thought it would be fun and good practice with Go. Also, I run a
small IRC network, and it would be nice to have my own server for it. Right now
I use ircd-ratbox.

I call this server catbox. I went with the name for a few reasons: My domain
name is summercat.com so I already have a cat reference. Cats love boxes. And
because of another ircd I like a lot is called ratbox!

The main ideas I plan for it are (in no particular order):

  * Server to server connections (to other instances)
  * Server to server connections (to ircd-ratbox)
  * Only a subset of RFC 2812 which I personally think makes sense. Only what
  * is critical for a minimal IRC server. As simple as possible. If the
    RFC suggests something I don't like, and I think clients will be compliant,
    then I'll probably do something else. I'll try to track differences.
  * TLS
  * Upgrade without losing connections
  * Minimal configuration
  * Simple and easily extensible
  * Server to server connections allowing server IPs to change without
    configuration updates (i.e., permitting dynamic server IPs)
  * Cool features as I come up with them. Some ideas I have:
    * Inform clients when someone whois's them.
    * Inform clients about TLS ciphers in use (both on connect and in their
      whois)
    * Bots could be built into the ircd
    * Private (very restricted whois, list, etc)


# Differences from RFC 2812

  * Only # channels supported.
  * Much more restricted characters in channels/nicks/users.
  * Do not support parameters to the LUSERS command.
  * Do not support parameters to the MOTD command.
  * Not supporting forwarding PING/PONG to other servers.
  * No wildcards or target server support in WHOIS command.
  * Added DIE command.
  * WHOIS command: No server target, and only single nicks.
  * WHOIS command: Currently not going to show any channels.
  * User modes: Only +oi
  * Channel modes: Only +ns
  * No channel ops or voices.
  * WHO: Support only 'WHO #channel'. And shows all nicks on that channel.
  * CONNECT: Single parameter only.
  * LINKS: No parameters supported.
  * LUSERS: Include +s channels in channel count.


# Docs/references

  * TS6 docs:
    * charybdis's ts6-protocol.txt
    * ircd-ratbox's ts6.txt, ts5.txt, README.TSora
  * ircv3
  * http://ircdocs.horse/


# TS notes

  * Nick TS changes when: Client connects or when it changes its nick.
  * Channel TS changes when: Channel created
  * Server to server (ircd-ratbox) commands I'm most interested in
    * Burst: SID, UID, SJOIN, ERROR, PING, PONG
    * Post-burst: INVITE, JOIN, KILL, NICK, NOTICE, PART, PRIVMSG, QUIT, SID,
      SJOIN, TOPIC, UID, SQUIT, ERROR, PING, PONG, MODE (user)


# Todo

  * Server to server (ircd-ratbox)
    * Post-burst: TOPIC, SQUIT, KILL, KLINE
    * Nick collisions
  * Drop messageUser/messageFromServer? messageUser all together,
    messageFromServer to be reply()?
  * TLS
  * Auto try/retry linking to servers if not connected
  * Daemonize
  * Log to file


## Maybe

  * LIST
  * Channel keys
  * INVITE
  * KICK
  * NAMES
  * VERSION
  * STATS
  * TIME
  * ADMIN
  * INFO
  * WHOWAS
  * AWAY
  * Multi line motd
  * Reload configuration without restart
  * Upgrade in place (is this possible with TLS connections?)
  * Server console.
  * Anti-abuse (throttling etc)

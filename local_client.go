package terrarium

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/horgh/irc"
)

// LocalClient holds state about a local connection.
// All connections are in this state until they register as either a user client
// or as a server.
type LocalClient struct { // nolint: maligned
	// Conn is the TCP connection to the client.
	Conn Conn

	// Their hostname. May be blank if we can't look it up.
	Hostname string

	// Locally unique identifier.
	ID uint64

	// WriteChan is the channel to send to to write to the client.
	WriteChan chan irc.Message

	// The time they connected.
	ConnectionStartTime time.Time

	// A reference to the main server.
	Catbox *Catbox

	// Track if we overflow our send queue. If we do, we'll kill the client.
	SendQueueExceeded bool

	// Track how many messages we receive in a pre-registered state.
	// If we hit a defined threshold, kill the connection.
	PreRegisterMessageCount int

	// Info client may send us before we complete its registration and promote it
	// to a user or server.

	// User info

	// NICK arguments.
	PreRegDisplayNick string

	// USER arguments.
	PreRegUser     string
	PreRegRealName string

	// Server info

	// PASS arguments.
	PreRegPass   string
	PreRegTS6SID string

	// CAPAB arguments.
	PreRegCapabs map[string]struct{}

	// SERVER arguments.
	PreRegServerName string
	PreRegServerDesc string

	// Boolean flags involved in the server link process. Use them to keep track
	// of where we are in the process.

	GotPASS   bool
	GotCAPAB  bool
	GotSERVER bool

	SentSERVER bool
	SentSVINFO bool
}

// MaxAllowedPreRegisterMessageCount defines how many messages a client may send
// us before registration before we consider them abusive and cut them off.
const MaxAllowedPreRegisterMessageCount = 10

// NewLocalClient creates a LocalClient
func NewLocalClient(cb *Catbox, id uint64, conn net.Conn) *LocalClient {
	return &LocalClient{
		Conn: NewConn(conn, cb.Config.DeadTime),
		ID:   id,

		// Buffered channel. We don't want to block sending to the client from the
		// server. The client may be stuck. Make the buffer large enough that it
		// should only max out in case of connection issues.
		WriteChan: make(chan irc.Message, 32768),

		ConnectionStartTime: time.Now(),
		Catbox:              cb,
		PreRegCapabs:        make(map[string]struct{}),
	}
}

func (c *LocalClient) String() string {
	return fmt.Sprintf("%d %s", c.ID, c.Conn.RemoteAddr())
}

// Determine if the client is using a TLS connection or not.
func (c *LocalClient) isTLS() bool {
	_, ok := c.Conn.conn.(*tls.Conn)
	return ok
}

// If the client is using a TLS connection, then this function gets its TLS
// version and ciphersuite as human readable strings.
//
// We run the client/server handshake if it has not been run yet.
func (c *LocalClient) getTLSState() (string, string, error) {
	tlsConn, ok := c.Conn.conn.(*tls.Conn)
	if !ok {
		return "", "", fmt.Errorf("client is not connected with TLS")
	}

	// Handshake() will read. If we don't have a timeout, we can get stuck here.
	if err := c.Conn.conn.SetDeadline(time.Now().Add(c.Conn.ioWait)); err != nil {
		return "", "", fmt.Errorf("error setting deadline: %s", err)
	}

	if err := tlsConn.Handshake(); err != nil {
		return "", "", fmt.Errorf("TLS handshake failed: %s", err)
	}

	state := tlsConn.ConnectionState()

	return tlsVersionToString(state.Version),
		cipherSuiteToString(state.CipherSuite), nil
}

// Send a message to the client. We send it to its write channel, which in turn
// leads to writing it to its TCP socket.
//
// This function won't block. If the client's queue is full, we flag it as
// having a full send queue.
//
// Not blocking is important because the server sends the client messages this
// way, and if we block on a problem client, everything would grind to a halt.
func (c *LocalClient) maybeQueueMessage(m irc.Message) {
	if c.SendQueueExceeded {
		return
	}

	select {
	case c.WriteChan <- m:
	default:
		c.SendQueueExceeded = true
	}
}

// readLoop endlessly reads from the client's TCP connection. It parses each
// IRC protocol message and passes it to the server through the server's
// channel.
func (c *LocalClient) readLoop() {
	defer c.Catbox.WG.Done()

	for {
		if c.Catbox.isShuttingDown() {
			break
		}

		buf, err := c.Conn.Read()
		if err != nil {
			log.Printf("Client %s: Read problem: %s", c, err)
			// Debug concerns with missing quit messages.
			if buf != "" {
				c.Catbox.noticeOpers(fmt.Sprintf("Read error but have [%s]",
					strings.TrimSpace(buf)))
			}
			c.Catbox.newEvent(Event{Type: DeadClientEvent, Client: c, Error: err})
			break
		}

		message, err := irc.ParseMessage(buf)
		if err != nil {
			c.Catbox.noticeOpers(fmt.Sprintf("Invalid message from client %s: %s", c,
				err))

			if err != irc.ErrTruncated {
				// Should we reply to the client? This silently ignores malformed
				// messages.
				continue
			}
		}

		c.Catbox.newEvent(Event{
			Type:    MessageFromClientEvent,
			Client:  c,
			Message: message,
		})
	}

	log.Printf("Client %s: Reader shutting down.", c)
}

// writeLoop endlessly reads from the client's channel, encodes each message,
// and writes it to the client's TCP connection.
//
// When the channel is closed, or if we have a write error, close the TCP
// connection. I have this here so that we try to deliver messages to the
// client before closing its socket and giving up.
func (c *LocalClient) writeLoop() {
	defer c.Catbox.WG.Done()

	// Receive on the client's write channel.
	//
	// Ensure we also stop if the server is shutting down (indicated by the
	// ShutdownChan being closed). If we don't, then there is potential for us to
	// leak this goroutine. Consider the case where we have a new client, and
	// tell the server about it, but the server is shutting down, and so does not
	// see the new client event. In this case the server does not know that it
	// must close the write channel so that the client will end (if we were for
	// example using 'for message := range c.WriteChan', as it would block
	// forever).
	//
	// A problem with this is we are not guaranteed to process any remaining
	// messages on the write channel (and so inform the client about shutdown)
	// when we are shutting down. But it is an improvement on leaking the
	// goroutine.
Loop:
	for {
		select {
		case message, ok := <-c.WriteChan:
			if !ok {
				break Loop
			}

			buf, err := message.Encode()
			if err != nil {
				c.Catbox.noticeOpers(fmt.Sprintf(
					"Trying to send invalid message to client %s: %s", c, err))
				if err != irc.ErrTruncated {
					continue
				}
			}

			if err := c.Conn.Write(buf); err != nil {
				log.Printf("Client %s: Write problem: %s: %s", c, buf, err)
				// Don't kill the client immediately. Give a chance for us to read
				// anything from it.
				time.Sleep(5 * time.Second)
				c.Catbox.newEvent(Event{Type: DeadClientEvent, Client: c, Error: err})
				break Loop
			}
		case <-c.Catbox.ShutdownChan:
			break Loop
		}
	}

	if err := c.Conn.Close(); err != nil {
		log.Printf("Client %s: Problem closing connection: %s", c, err)
	}

	log.Printf("Client %s: Writer shutting down.", c)
}

// quit means the client is quitting. Tell it why and clean up.
func (c *LocalClient) quit(msg string) {
	// May already be cleaning up.
	_, exists := c.Catbox.LocalClients[c.ID]
	if !exists {
		return
	}

	c.messageFromServer("ERROR", []string{msg})

	close(c.WriteChan)

	delete(c.Catbox.LocalClients, c.ID)
}

// Upgrade a LocalClient to a LocalUser.
func (c *LocalClient) registerUser() {
	// RFC 2813 specifies messages to send upon registration.

	// Check NICK is still available. I'm no longer reserving it in the Nicks map
	// until registration completes, so check now.
	_, exists := c.Catbox.Nicks[canonicalizeNick(c.PreRegDisplayNick)]
	if exists {
		// 433 ERR_NICKNAMEINUSE
		c.messageFromServer("433", []string{c.PreRegDisplayNick,
			"Nickname is already in use"})
		return
	}

	lu := NewLocalUser(c)

	// This IP field is not always actually an IP. It can be "0" in the case of a
	// user with a spoof (this is specified in TS6). It also gets sent in a UID
	// command, and not as the last parameter . Because of that, we must make
	// sure it does not start with ":" as that cannot be encoded. Consider IPv6
	// IPs such as "::1". TS6 specifies that with these we prepend a "0". e.g.,
	// "0::1".
	ip := c.Conn.IP.String()
	if ip[0] == ':' {
		ip = "0" + ip
	}

	hostname := ip
	if len(c.Hostname) > 0 {
		hostname = c.Hostname
	}

	u := &User{
		DisplayNick: c.PreRegDisplayNick,
		HopCount:    0,
		NickTS:      time.Now().Unix(),
		Modes:       make(map[byte]struct{}),
		Username:    c.PreRegUser,
		Hostname:    hostname,
		IP:          ip,
		RealName:    c.PreRegRealName,
		Channels:    make(map[string]*Channel),
		LocalUser:   lu,
	}

	lu.User = u

	// Apply any user configuration that matches them.
	// This may flag the user flood exempt.
	// This may give the user a spoof.
	for _, userConfig := range c.Catbox.Config.UserConfigs {
		if !u.matchesMask(userConfig.UserMask, userConfig.HostMask) {
			continue
		}

		u.FloodExempt = userConfig.FloodExempt
		if u.FloodExempt {
			lu.serverNotice("Congratulations. You're exempt from flood protection.")
		}

		if len(userConfig.Spoof) > 0 {
			u.Hostname = userConfig.Spoof
			lu.serverNotice(fmt.Sprintf("Spoofing your hostname as %s", u.Hostname))
		}

		// Match the first only.
		break
	}

	// Check if they're klined. Don't accept further if so.
	for _, kline := range c.Catbox.KLines {
		if !u.matchesMask(kline.UserMask, kline.HostMask) {
			continue
		}
		// 465 ERR_YOUREBANNEDCREEP
		lu.messageFromServer("465", []string{"You are banned from this server"})

		c.quit(fmt.Sprintf("Connection closed: %s", kline.Reason))

		c.Catbox.noticeLocalOpers(fmt.Sprintf(
			"Rejecting user registration for %s!%s@%s. KLined: %s",
			u.DisplayNick, u.Username, u.Hostname, kline.Reason))
		return
	}

	uid, err := lu.makeTS6UID(lu.ID)
	if err != nil {
		log.Fatal(err)
	}
	u.UID = uid

	delete(c.Catbox.LocalClients, c.ID)
	c.Catbox.LocalUsers[lu.ID] = lu
	c.Catbox.Nicks[canonicalizeNick(u.DisplayNick)] = u.UID
	c.Catbox.Users[u.UID] = u

	// 001 RPL_WELCOME
	lu.messageFromServer("001", []string{
		fmt.Sprintf("Welcome to the Internet Relay Network %s", u.nickUhost()),
	})

	// 002 RPL_YOURHOST
	lu.messageFromServer("002", []string{
		fmt.Sprintf("Your host is %s, running version %s",
			lu.Catbox.Config.ServerName,
			lu.Catbox.version(),
		),
	})

	// 003 RPL_CREATED
	lu.messageFromServer("003", []string{
		fmt.Sprintf("This server was created %s", CreatedDate),
	})

	// 004 RPL_MYINFO
	// <servername> <version> <available user modes> <available channel modes>
	lu.messageFromServer("004", []string{
		// It seems ambiguous if these are to be separate parameters.
		lu.Catbox.Config.ServerName,
		lu.Catbox.version(),
		// User modes we support.
		"ioC",
		// Channel modes we support.
		"nos",
	})

	c.Catbox.updateCounters()
	c.Catbox.ConnectionCount++

	lu.lusersCommand()
	lu.motdCommand()

	// Set user mode +i automatically.
	lu.messageUser(u, "MODE", []string{u.DisplayNick, "+i"})
	u.Modes['i'] = struct{}{}

	// Tell linked servers about this new client.
	for _, server := range c.Catbox.LocalServers {
		server.maybeQueueMessage(irc.Message{
			Prefix:  string(c.Catbox.Config.TS6SID),
			Command: "UID",
			Params: []string{
				u.DisplayNick,
				// Hop count increases for them.
				fmt.Sprintf("%d", u.HopCount+1),
				fmt.Sprintf("%d", u.NickTS),
				u.modesString(),
				u.Username,
				u.Hostname,
				u.IP,
				string(u.UID),
				u.RealName,
			},
		})

		// Send a CLICONN message. This is a custom command I built into ratbox
		// so that local opers can know about remote connections. For terrarium we
		// don't need to handle this to know about remote connections as I inform
		// local operators about remote users connecting in the UID command, but to
		// allow my ratbox servers to know about connections to ratbox, send CLICONN
		// (for now). If I ever stop running all ratbox servers on my network, this
		// can be removed.
		// terrarium should propagate this command though.
		server.maybeQueueMessage(irc.Message{
			Prefix:  string(u.UID),
			Command: "CLICONN",
			Params:  []string{c.Catbox.Config.ServerName, u.IP},
		})
	}

	// Tell local operators.
	// Remote operators can know as their server will receive a UID command, so
	// their server can tell them upon receipt of that.
	for _, oper := range c.Catbox.Opers {
		if !oper.isLocal() {
			continue
		}
		_, exists := oper.Modes['C']
		if !exists {
			continue
		}
		oper.LocalUser.serverNotice(fmt.Sprintf("CLICONN %s %s %s %s %s (%s)",
			u.DisplayNick, u.Username, u.Hostname, u.IP, u.RealName,
			c.Catbox.Config.ServerName))
	}
}

// Send an IRC message to a client. Appears to be from the server.
// This works by writing to a client's channel.
//
// Note: Only the server goroutine should call this (due to channel use).
func (c *LocalClient) messageFromServer(command string, params []string) {
	// For numeric messages, we need to prepend the nick.
	// Use * for the nick in cases where the client doesn't have one yet.
	// This is what ircd-ratbox does. Maybe not RFC...
	if isNumericCommand(command) {
		nick := "*"
		if len(c.PreRegDisplayNick) > 0 {
			nick = c.PreRegDisplayNick
		}
		newParams := []string{nick}
		newParams = append(newParams, params...)
		params = newParams
	}

	c.maybeQueueMessage(irc.Message{
		Prefix:  c.Catbox.Config.ServerName,
		Command: command,
		Params:  params,
	})
}

func (c *LocalClient) sendSVINFO() {
	// SVINFO <TS version> <min TS version> 0 <current time>
	epoch := time.Now().Unix()
	c.maybeQueueMessage(irc.Message{
		Command: "SVINFO",
		Params: []string{
			"6", "6", "0", fmt.Sprintf("%d", epoch),
		},
	})

	c.SentSVINFO = true
}

// Upgrade a LocalClient to a LocalServer.
func (c *LocalClient) registerServer() {
	newLS := NewLocalServer(c)

	newServer := &Server{
		SID:         TS6SID(c.PreRegTS6SID),
		Name:        c.PreRegServerName,
		Description: c.PreRegServerDesc,
		HopCount:    1,
		Capabs:      c.PreRegCapabs,
		LocalServer: newLS,
	}

	newLS.Server = newServer

	delete(c.Catbox.LocalClients, c.ID)
	c.Catbox.LocalServers[newLS.ID] = newLS
	c.Catbox.Servers[newServer.SID] = newServer

	linkNotice := ""
	if c.isTLS() {
		tlsVersion, tlsCipherSuite, err := c.getTLSState()
		if err != nil {
			c.quit(fmt.Sprintf("Unable to determine TLS information: %s", err))
			return
		}

		linkNotice = fmt.Sprintf("Established link to %s with %s (%s).",
			c.PreRegServerName, tlsVersion, tlsCipherSuite)
	} else {
		linkNotice = fmt.Sprintf("Established link to %s (PLAINTEXT).",
			c.PreRegServerName)
	}

	c.Catbox.ConnectionCount++

	newLS.Catbox.noticeOpers(linkNotice)

	newLS.sendBurst()

	// PING <My SID>
	newLS.maybeQueueMessage(irc.Message{
		Command: "PING",
		Params:  []string{string(c.Catbox.Config.TS6SID)},
	})

	// Tell connected servers about the new server.
	// :<my SID> SID <server name> <hop count> <SID> <description>
	// e.g.: :8ZZ SID irc3.example.com 2 9ZQ :My Desc
	for _, ls := range c.Catbox.LocalServers {
		// We don't have to tell the server about itself.
		if ls == newLS {
			continue
		}

		ls.maybeQueueMessage(irc.Message{
			// It's linked to us, so set prefix to ourself.
			Prefix:  string(c.Catbox.Config.TS6SID),
			Command: "SID",
			Params: []string{
				newServer.Name,
				fmt.Sprintf("%d", newServer.HopCount+1),
				string(newServer.SID),
				newServer.Description,
			},
		})

		// Also tell them about its capabs.
		ls.maybeQueueMessage(irc.Message{
			Prefix:  string(newServer.SID),
			Command: "ENCAP",
			Params:  []string{"*", "GCAP", newServer.capabsString()},
		})
	}
}

func (c *LocalClient) sendServerIntro(pass string) {
	// PASS <password>, TS, <ts version>, <SID>
	c.maybeQueueMessage(irc.Message{
		Command: "PASS",
		Params: []string{
			pass, "TS", "6", string(c.Catbox.Config.TS6SID)},
	})

	// CAPAB <space separated list>
	c.maybeQueueMessage(irc.Message{
		Command: "CAPAB",
		// QS means quitstorm. This means we don't need to hear QUITs from servers
		// that are delinking (AFAICT) -- that we can figure it out ourselves and
		// generate the QUITs ourself locally (see client.c in ircd-ratbox).
		// ENCAP means support for the ENCAP command. See
		// http://www.leeh.co.uk/ircd/encap.txt
		// TB means support for topic burst. We send/receive TB commands during
		// burst which tells the topics in channels.
		Params: []string{"QS ENCAP TB"},
	})

	// SERVER <name> <hopcount> <description>
	c.maybeQueueMessage(irc.Message{
		Command: "SERVER",
		Params: []string{
			c.Catbox.Config.ServerName,
			"1",
			c.Catbox.Config.ServerInfo,
		},
	})
	c.SentSERVER = true
}

// The client sent us a message. Deal with it.
func (c *LocalClient) handleMessage(m irc.Message) {
	// Clients SHOULD NOT (section 2.3) send a prefix.
	if m.Prefix != "" {
		c.quit("No prefix permitted")
		return
	}

	// If they send too many messages before registering, assume they are abusive
	// and kill their connection.
	c.PreRegisterMessageCount++
	if c.PreRegisterMessageCount >= MaxAllowedPreRegisterMessageCount {
		c.quit("Too many messages")
		return
	}

	// Non-RFC command that appears to be widely supported. Just ignore it for
	// now.
	if m.Command == "CAP" {
		return
	}

	// We may receive NOTICE when initiating connection to a server. Ignore it.
	if m.Command == "NOTICE" {
		return
	}

	// To register as a user client:
	// NICK
	// USER

	if m.Command == "NICK" {
		c.nickCommand(m)
		return
	}

	if m.Command == "USER" {
		c.userCommand(m)
		return
	}

	// To register as a server (using TS6):

	// If incoming client is initiator, they send this:

	// > PASS
	// > CAPAB
	// > SERVER

	// We check this info. If valid, reply:

	// < PASS
	// < CAPAB
	// < SERVER

	// They check our info. If valid, reply:

	// > SVINFO

	// We reply again:

	// < SVINFO
	// < Burst
	// < PING

	// They finish:

	// > Burst
	// > PING

	// Everyone ACKs the PINGs:

	// < PONG

	// > PONG

	// PINGs are used to know end of burst. Then we're linked.

	// If we initiate the link, then we send PASS/CAPAB/SERVER and expect it
	// in return. Beyond that, the process is the same.

	if m.Command == "PASS" {
		c.passCommand(m)
		return
	}

	if m.Command == "CAPAB" {
		c.capabCommand(m)
		return
	}

	if m.Command == "SERVER" {
		c.serverCommand(m)
		return
	}

	if m.Command == "SVINFO" {
		c.svinfoCommand(m)
		return
	}

	if m.Command == "ERROR" {
		c.errorCommand(m)
		return
	}

	// Let's say *all* other commands require you to be registered.
	// 451 ERR_NOTREGISTERED
	c.messageFromServer("451", []string{fmt.Sprintf("You have not registered.")})
}

// The NICK command to happen both at connection registration time and
// after. There are different rules.
func (c *LocalClient) nickCommand(m irc.Message) {
	// We should have one parameter: The nick they want.
	if len(m.Params) == 0 {
		// 431 ERR_NONICKNAMEGIVEN
		c.messageFromServer("431", []string{"No nickname given"})
		return
	}
	nick := m.Params[0]

	if len(nick) > c.Catbox.Config.MaxNickLength {
		nick = nick[0:c.Catbox.Config.MaxNickLength]
	}

	if !isValidNick(c.Catbox.Config.MaxNickLength, nick) {
		// 432 ERR_ERRONEUSNICKNAME
		c.messageFromServer("432", []string{nick, "Erroneous nickname"})
		return
	}

	nickCanon := canonicalizeNick(nick)

	// Nick must be unique.
	_, exists := c.Catbox.Nicks[nickCanon]
	if exists {
		// 433 ERR_NICKNAMEINUSE
		c.messageFromServer("433", []string{nick, "Nickname is already in use"})
		return
	}

	// NOTE: I no longer flag the nick as taken until registration completes.
	//   Simpler.

	c.PreRegDisplayNick = nick

	// We don't reply during registration (we don't have enough info, no uhost
	// anyway).

	// If we have USER done already, then we're done registration.
	if len(c.PreRegUser) > 0 {
		c.registerUser()
	}
}

func (c *LocalClient) userCommand(m irc.Message) {
	// RFC RECOMMENDs NICK before USER. But I'm going to allow either way now.
	// One reason to do so is how to react if NICK was taken and client
	// proceeded to USER.

	// 4 parameters: <user> <mode> <unused> <realname>
	if len(m.Params) != 4 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{m.Command, "Not enough parameters"})
		return
	}

	// I don't check ident. So we always prefix ~. Add it here before we check
	// length to ensure length includes it.
	user := "~" + m.Params[0]

	if len(user) > maxUsernameLength {
		user = user[0:maxUsernameLength]
	}

	if !isValidUser(user) {
		// There isn't an appropriate response in the RFC. ircd-ratbox sends an
		// ERROR message. Do that.
		c.messageFromServer("ERROR", []string{"Invalid username"})
		return
	}
	c.PreRegUser = user

	// We could do something with user mode here.

	realName := m.Params[3]
	if len(realName) > maxRealNameLength {
		realName = realName[:maxRealNameLength]
	}

	if !isValidRealName(realName) {
		c.messageFromServer("ERROR", []string{"Invalid realname"})
		return
	}
	c.PreRegRealName = realName

	// If we have a nick, then we're done registration.
	if len(c.PreRegDisplayNick) > 0 {
		c.registerUser()
	}
}

func (c *LocalClient) passCommand(m irc.Message) {
	// For server registration:
	// PASS <password>, TS, <ts version>, <SID>
	if len(m.Params) < 4 {
		// For now I only recognise this form of PASS.
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"PASS", "Not enough parameters"})
		return
	}

	if c.GotPASS {
		c.quit("Double PASS")
		return
	}

	// We can't validate password yet.

	if m.Params[1] != "TS" {
		c.quit("Unexpected PASS format: TS")
		return
	}

	tsVersion, err := strconv.ParseInt(m.Params[2], 10, 64)
	if err != nil {
		c.quit("Unexpected PASS format: Version: " + err.Error())
		return
	}

	// Support only TS 6.
	if tsVersion != 6 {
		c.quit("Unsupported TS version")
		return
	}

	// Beyond format, we can't validate SID yet.
	if !isValidSID(m.Params[3]) {
		c.quit("Malformed SID")
		return
	}
	sid := TS6SID(m.Params[3])
	if sid == c.Catbox.Config.TS6SID {
		c.quit("You're using my SID!")
		return
	}
	if _, ok := c.Catbox.Servers[sid]; ok {
		c.quit("I already know that SID!")
		return
	}

	// Everything looks OK. Store them.

	c.PreRegPass = m.Params[0]
	c.PreRegTS6SID = m.Params[3]

	c.GotPASS = true

	// Don't reply yet.
}

func (c *LocalClient) capabCommand(m irc.Message) {
	// CAPAB <space separated list>
	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"CAPAB", "Not enough parameters"})
		return
	}

	if !c.GotPASS {
		c.quit("PASS first")
		return
	}

	if c.GotCAPAB {
		c.quit("Double CAPAB")
		return
	}

	c.PreRegCapabs = parseCapabsString(m.Params[0])

	// For TS6 we must have QS and ENCAP.

	_, exists := c.PreRegCapabs["QS"]
	if !exists {
		c.quit("Missing QS")
		return
	}

	_, exists = c.PreRegCapabs["ENCAP"]
	if !exists {
		c.quit("Missing ENCAP")
		return
	}

	c.GotCAPAB = true
}

func (c *LocalClient) serverCommand(m irc.Message) {
	// SERVER <name> <hopcount> <description>
	if len(m.Params) != 3 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"SERVER", "Not enough parameters"})
		return
	}

	if !c.GotCAPAB {
		c.quit("CAPAB first.")
		return
	}

	if c.GotSERVER {
		c.quit("Double SERVER.")
		return
	}

	serverName := m.Params[0]

	// We could validate the hostname format. But we have a list of hosts we will
	// link to, so check against that directly.
	linkInfo, exists := c.Catbox.Config.Servers[serverName]
	if !exists {
		c.quit("I don't know you")
		return
	}

	// At this point we should have a password from the PASS command. Check it.
	if linkInfo.Pass != c.PreRegPass {
		c.quit("Bad password")
		return
	}

	// Hopcount should be 1.
	if m.Params[1] != "1" {
		c.quit("Bad hopcount")
		return
	}

	// Is this server already linked?
	if c.Catbox.isLinkedToServer(serverName) {
		c.quit("I'm already linked to you!")
		return
	}

	c.PreRegServerName = serverName
	c.PreRegServerDesc = m.Params[2]

	c.GotSERVER = true

	// Reply. Our reply differs depending on whether we initiated the link.

	// If they initiated the link, then we reply with PASS/CAPAB/SERVER.
	// If we did, then we already sent PASS/CAPAB/SERVER. Reply with SVINFO
	// instead.

	if !c.SentSERVER {
		c.sendServerIntro(linkInfo.Pass)

		return
	}

	c.sendSVINFO()
}

func (c *LocalClient) svinfoCommand(m irc.Message) {
	// SVINFO <TS version> <min TS version> 0 <current time>
	if len(m.Params) < 4 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"SVINFO", "Not enough parameters"})
		return
	}

	if !c.GotSERVER || !c.SentSERVER {
		c.quit("SERVER first")
		return
	}

	// Once we have SVINFO, we'll upgrade to LocalServer, so we will never see
	// double SVINFO.

	if m.Params[0] != "6" || m.Params[1] != "6" {
		c.quit("Unsupported TS version")
		return
	}

	if m.Params[2] != "0" {
		c.quit("Malformed third parameter")
		return
	}

	theirEpoch, err := strconv.ParseInt(m.Params[3], 10, 64)
	if err != nil {
		c.quit("Malformed time")
		return
	}

	epoch := time.Now().Unix()

	delta := epoch - theirEpoch
	if delta < 0 {
		delta *= -1
	}

	if delta > 60 {
		c.quit("Time insanity")
		return
	}

	// Final check that we're not linked to this server.
	if c.Catbox.isLinkedToServer(c.PreRegServerName) {
		c.quit("I'm already linked to you!")
		return
	}

	// If we initiated the connection, then we already sent SVINFO (in reply
	// to them sending SERVER). This is their reply to our SVINFO.
	if !c.SentSVINFO {
		c.sendSVINFO()
	}

	// Let's choose here to decide we're linked. The burst is still to come.
	c.registerServer()
}

func (c *LocalClient) errorCommand(m irc.Message) {
	c.quit("Bye")
}

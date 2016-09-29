package main

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"summercat.com/irc"
)

// LocalClient holds state about a local connection.
// All connections are in this state until they register as either a user client
// or as a server.
type LocalClient struct {
	// Conn is the TCP connection to the client.
	Conn Conn

	// Locally unique identifier.
	ID uint64

	// WriteChan is the channel to send to to write to the client.
	WriteChan chan irc.Message

	ConnectionStartTime time.Time

	Catbox *Catbox

	// Track if we overflow our send queue. If we do, we'll kill the client.
	SendQueueExceeded bool

	// Info client may send us before we complete its registration and promote it
	// to a user or server.

	// User info

	// NICK
	PreRegDisplayNick string

	// USER
	PreRegUser     string
	PreRegRealName string

	// Server info

	// PASS
	PreRegPass   string
	PreRegTS6SID string

	// CAPAB
	PreRegCapabs map[string]struct{}

	// SERVER
	PreRegServerName string
	PreRegServerDesc string

	// Boolean flags involved in the server link process. Use them to keep track
	// of where we are in the process.

	GotPASS   bool
	GotCAPAB  bool
	GotSERVER bool

	SentPASS   bool
	SentCAPAB  bool
	SentSERVER bool
	SentSVINFO bool
}

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

// readLoop endlessly reads from the client's TCP connection. It parses each
// IRC protocol message and passes it to the server through the server's
// channel.
func (c *LocalClient) readLoop() {
	defer c.Catbox.WG.Done()

	for {
		if c.Catbox.isShuttingDown() {
			break
		}

		// This means if a client sends us an invalid message that we cut them off.
		message, err := c.Conn.ReadMessage()
		if err != nil {
			log.Printf("Client %s: %s", c, err)
			c.Catbox.newEvent(Event{Type: DeadClientEvent, Client: c})
			break
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

			err := c.Conn.WriteMessage(message)
			if err != nil {
				log.Printf("Client %s: %s", c, err)
				c.Catbox.newEvent(Event{Type: DeadClientEvent, Client: c})
				break Loop
			}
		case <-c.Catbox.ShutdownChan:
			break Loop
		}
	}

	err := c.Conn.Close()
	if err != nil {
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

	u := &User{
		DisplayNick: c.PreRegDisplayNick,
		NickTS:      time.Now().Unix(),
		Modes:       make(map[byte]struct{}),
		Username:    c.PreRegUser,
		Hostname:    fmt.Sprintf("%s", c.Conn.RemoteAddr()),
		IP:          fmt.Sprintf("%s", c.Conn.RemoteAddr()),
		RealName:    c.PreRegRealName,
		Channels:    make(map[string]*Channel),
		LocalUser:   lu,
	}

	lu.User = u

	uid, err := lu.makeTS6UID(lu.ID)
	if err != nil {
		log.Fatal(err)
	}
	u.UID = uid

	delete(c.Catbox.LocalClients, c.ID)
	c.Catbox.LocalUsers[lu.ID] = lu
	c.Catbox.Users[u.UID] = u

	// TODO: Tell linked servers about this new client.

	// 001 RPL_WELCOME
	lu.messageFromServer("001", []string{
		fmt.Sprintf("Welcome to the Internet Relay Network %s", u.nickUhost()),
	})

	// 002 RPL_YOURHOST
	lu.messageFromServer("002", []string{
		fmt.Sprintf("Your host is %s, running version %s",
			lu.Catbox.Config.ServerName,
			lu.Catbox.Config.Version),
	})

	// 003 RPL_CREATED
	lu.messageFromServer("003", []string{
		fmt.Sprintf("This server was created %s", lu.Catbox.Config.CreatedDate),
	})

	// 004 RPL_MYINFO
	// <servername> <version> <available user modes> <available channel modes>
	lu.messageFromServer("004", []string{
		// It seems ambiguous if these are to be separate parameters.
		lu.Catbox.Config.ServerName,
		lu.Catbox.Config.Version,
		"i",
		"ns",
	})

	lu.lusersCommand()
	lu.motdCommand()
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

func (c *LocalClient) sendPASS(pass string) {
	// PASS <password>, TS, <ts version>, <SID>
	c.maybeQueueMessage(irc.Message{
		Command: "PASS",
		Params:  []string{pass, "TS", "6", c.Catbox.Config.TS6SID},
	})

	c.SentPASS = true
}

func (c *LocalClient) sendCAPAB() {
	// CAPAB <space separated list>
	c.maybeQueueMessage(irc.Message{
		Command: "CAPAB",
		Params:  []string{"QS ENCAP"},
	})

	c.SentCAPAB = true
}

func (c *LocalClient) sendSERVER() {
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

func (c *LocalClient) registerServer() {
	ls := NewLocalServer(c)

	s := &Server{
		SID:         TS6SID(c.PreRegTS6SID),
		Name:        c.PreRegServerName,
		Description: c.PreRegServerDesc,
		LocalServer: ls,
	}

	ls.Server = s

	delete(c.Catbox.LocalClients, c.ID)
	c.Catbox.LocalServers[ls.ID] = ls
	c.Catbox.Servers[s.SID] = s

	ls.Catbox.noticeOpers(fmt.Sprintf("Established link to %s.",
		c.PreRegServerName))

	ls.sendBurst()
	ls.sendPING()
}

func (c *LocalClient) isSendQueueExceeded() bool {
	return c.SendQueueExceeded
}

func (c *LocalClient) handleMessage(m irc.Message) {
	// Clients SHOULD NOT (section 2.3) send a prefix.
	if m.Prefix != "" {
		c.quit("No prefix permitted")
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

	user := m.Params[0]

	if len(user) > c.Catbox.Config.MaxNickLength {
		user = user[0:c.Catbox.Config.MaxNickLength]
	}

	if !isValidUser(c.Catbox.Config.MaxNickLength, user) {
		// There isn't an appropriate response in the RFC. ircd-ratbox sends an
		// ERROR message. Do that.
		c.messageFromServer("ERROR", []string{"Invalid username"})
		return
	}
	c.PreRegUser = user

	// We could do something with user mode here.

	// Validate realname.
	// Arbitrary. Length only.
	if len(m.Params[3]) > 64 {
		c.messageFromServer("ERROR", []string{"Invalid realname"})
		return
	}
	c.PreRegRealName = m.Params[3]

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

	capabs := strings.Split(m.Params[0], " ")

	// No real validation to do on these right now. Just record them.

	for _, cap := range capabs {
		cap = strings.TrimSpace(cap)
		if len(cap) == 0 {
			continue
		}

		cap = strings.ToUpper(cap)

		c.PreRegCapabs[cap] = struct{}{}
	}

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

	// We could validate the hostname format. But we have a list of hosts we will
	// link to, so check against that directly.
	linkInfo, exists := c.Catbox.Config.Servers[m.Params[0]]
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
	_, exists = c.Catbox.Servers[TS6SID(m.Params[0])]
	if exists {
		c.quit("Already linked")
		return
	}

	c.PreRegServerName = m.Params[0]
	c.PreRegServerDesc = m.Params[2]

	c.GotSERVER = true

	// Reply. Our reply differs depending on whether we initiated the link.

	// If they initiated the link, then we reply with PASS/CAPAB/SERVER.
	// If we did, then we already sent PASS/CAPAB/SERVER. Reply with SVINFO
	// instead.

	if !c.SentSERVER {
		c.sendPASS(linkInfo.Pass)
		c.sendCAPAB()
		c.sendSERVER()
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

	// Once we have SVINFO, we'll upgrade to ServerClient, so we will never see
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
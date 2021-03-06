package terrarium

import (
	"fmt"
	"log"
)

// User holds information about a user. It may be remote or local.
type User struct {
	// The user's nick. Formatted for display.
	DisplayNick string

	// The number of hops away the user is.
	HopCount int

	// The user's nick's TS. This changes on registration and NICK.
	NickTS int64

	// The user's modes. Currently +i, +o, +C supported.
	Modes map[byte]struct{}

	// The user's username.
	Username string

	// The user's hostname.
	Hostname string

	// The user's IP. Not always a valid looking IP (e.g. may be 0 if a spoofed
	// user sent to us from a different server).
	IP string

	// Each user has a network wide unique identifier. This is part of TS6.
	// It is 9 characters. The first 3 are the server it is on's SID.
	UID TS6UID

	// The user's real name (set with USER command on registration).
	RealName string

	// Away message. If blank, they're not away.
	AwayMessage string

	// Channel name (canonicalized) to Channel. The channels it is in.
	Channels map[string]*Channel

	// A user may be flagged as flood exempt. For example, if they match a user
	// record. They are also flood exempt if they are an operator. To check if
	// a user is flood exempt, use the isFloodExempt() function.
	FloodExempt bool

	// LocalUser set if this is a local user.
	LocalUser *LocalUser

	// This is the server we heard about the user from. It is not necessarily the
	// server they are on. It could be on a server linked to the one we are
	// linked to.
	ClosestServer *LocalServer

	// This is the server the user is connected to.
	Server *Server
}

func (u *User) String() string {
	return fmt.Sprintf("%s: %s", u.UID, u.nickUhost())
}

func (u *User) nickUhost() string {
	return fmt.Sprintf("%s!%s@%s", u.DisplayNick, u.Username, u.Hostname)
}

func (u *User) isOperator() bool {
	_, exists := u.Modes['o']
	return exists
}

// Is the user on the given channel?
func (u *User) onChannel(channel *Channel) bool {
	_, exists := u.Channels[channel.Name]
	return exists
}

// Make a string of their user modes. + if no modes.
func (u *User) modesString() string {
	s := "+"
	for m := range u.Modes {
		s += string(m)
	}
	return s
}

func (u *User) isLocal() bool {
	return u.LocalUser != nil
}

func (u *User) isRemote() bool {
	return !u.isLocal()
}

// Is a user flood exempt?
//
// If they are an oper, they are.
//
// If they are flagged so, they are.
func (u *User) isFloodExempt() bool {
	return u.isOperator() || u.FloodExempt
}

// Determine if our user mask (Username@Hostname) matches the given mask.
//
// If there are no wildcards in the mask, then it must match our user@host.
//
// We support glob style (*) wildcards and ? to match any single char.
func (u *User) matchesMask(userMask, hostMask string) bool {
	userRE, err := maskToRegex(userMask)
	if err != nil {
		log.Printf("matchesMask: %s", err)
		return false
	}
	if !userRE.MatchString(u.Username) {
		return false
	}

	hostRE, err := maskToRegex(hostMask)
	if err != nil {
		log.Printf("matchesMask: %s", err)
		return false
	}
	return hostRE.MatchString(u.Hostname)
}

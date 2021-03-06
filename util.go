package terrarium

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 50 from RFC
const maxChannelLength = 50

// Arbitrary. Something low enough we won't hit message limit.
const maxTopicLength = 300

// There is no limit defined in any RFC that I see. However, ratbox has username
// length hardcoded to 10, and truncates at that.
// It counts ~ in its length.
// This limit of 10 I do not see in any RFC. However, ratbox has it hardcoded.
const maxUsernameLength = 10

// This matches ratbox's.
const maxRealNameLength = 50

// ByHopCount is a sort type for sorting *Servers by their hop count
type ByHopCount []*Server

func (h ByHopCount) Len() int           { return len(h) }
func (h ByHopCount) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h ByHopCount) Less(i, j int) bool { return h[i].HopCount < h[j].HopCount }

// canonicalizeNick converts the given nick to its canonical representation
// (which must be unique). To get this, we transform all uppercase characters
// to lowercase.
//
// Note IRC has an odd opinion of what is upper and lowercase. In particular,
// {}|^ are considered the lowercase equivalents of []\~. However, I do not
// touch ~ as it is an invalid nickname character.
//
// Note: We don't check validity or strip whitespace.
func canonicalizeNick(n string) string {
	b := []byte(strings.ToLower(n))

	for i := 0; i < len(b); i++ {
		if b[i] == '[' {
			b[i] = '{'
			continue
		}
		if b[i] == ']' {
			b[i] = '}'
			continue
		}
		if b[i] == '\\' {
			b[i] = '|'
			continue
		}
	}

	return string(b)
}

// canonicalizeChannel converts the given channel to its canonical
// representation (which must be unique).
//
// Note: We don't check validity or strip whitespace.
func canonicalizeChannel(c string) string {
	return strings.ToLower(c)
}

// isValidNick checks if a nickname is valid.
//
// To be compatible with ratbox, I try to accept the same characters and apply
// similar restrictions as it does. See its m_nick.c clean_nick() function for
// its validation. This appears to be the nick format defined by RFC 2812. RFC
// 1459 has a more restricted set. In particular, RFC 1459 does not allow
// "special" characters to be in the first position in the nick, whereas RFC
// 2812 does.
func isValidNick(maxLen int, n string) bool {
	if len(n) == 0 || len(n) > maxLen {
		return false
	}

	// First character may not be - or 0-9.

	// Afterwards we permit these characters:
	// -0-9A-Z[\]^_`a-z{|}

	for i, char := range n {
		if i == 0 {
			if char == '-' {
				return false
			}

			if char >= '0' && char <= '9' {
				return false
			}
		}

		if char == '-' {
			continue
		}

		if char >= '0' && char <= '9' {
			continue
		}

		if char >= 'A' && char <= 'Z' {
			continue
		}

		if char == '[' || char == '\\' || char == ']' || char == '^' ||
			char == '_' || char == '`' {
			continue
		}

		if char >= 'a' && char <= 'z' {
			continue
		}

		if char == '{' || char == '|' || char == '}' {
			continue
		}

		return false
	}

	return true
}

// isValidUser checks if a user (USER command) is valid
//
// See valid_username() in ratbox's match.c to see what ratbox accepts. This
// function accepts the same characters as ratbox and enforces similar
// restrictions. This is still more restrictive than RFC 1459.
func isValidUser(u string) bool {
	if len(u) == 0 || len(u) > maxUsernameLength {
		return false
	}

	// How many dots we permit in the username.
	// They are never permitted as the final character.
	const dotsAllowed = 2

	if u[len(u)-1] == '.' {
		return false
	}

	dots := 0

	// First character is more restricted:
	// 0-9A-Za-z[\]^{|}~

	// After the first character, we permit additional chars. These are the
	// permitted characters:
	//
	// . is permitted (up to a limit)
	//
	// 0-9A-Za-z$-[\]^_`{|}~
	for i, char := range u {
		if i == 0 {
			if char >= '0' && char <= '9' {
				continue
			}

			if char >= 'A' && char <= 'Z' {
				continue
			}

			if char >= 'a' && char <= 'z' {
				continue
			}

			if char == '[' || char == '\\' || char == ']' || char == '^' ||
				char == '{' || char == '|' || char == '}' || char == '~' {
				continue
			}

			return false
		}

		if char == '.' {
			dots++

			if dots > dotsAllowed {
				return false
			}

			continue
		}

		if char >= '0' && char <= '9' {
			continue
		}

		if char >= 'A' && char <= 'Z' {
			continue
		}

		if char >= 'a' && char <= 'z' {
			continue
		}

		if char == '$' || char == '-' ||
			char == '[' || char == '\\' || char == ']' || char == '^' ||
			char == '_' || char == '`' ||
			char == '{' || char == '|' || char == '}' || char == '~' {
			continue
		}

		return false
	}

	return true
}

func isValidRealName(s string) bool {
	return len(s) <= maxRealNameLength
}

// isValidChannel checks a channel name for validity.
//
// You should canonicalize it before using this function.
func isValidChannel(c string) bool {
	if len(c) == 0 || len(c) > maxChannelLength {
		return false
	}

	// I accept only a-z or 0-9 as valid characters right now. RFC accepts more.
	for i, char := range c {
		if i == 0 {
			// I only allow # channels right now.
			if char == '#' {
				continue
			}
			return false
		}

		if char >= 'a' && char <= 'z' {
			continue
		}

		if char >= '0' && char <= '9' {
			continue
		}

		return false
	}

	return true
}

// isValidHostname is a basic check to determine if a host looks valid.
// Very basic.
func isValidHostname(s string) bool {
	matched, err := regexp.MatchString("^[A-Za-z0-9-.]+$", s)
	if err != nil {
		return false
	}
	return matched
}

// Check if a string is a valid user mask.
// This is a pattern with * or ? glob style characters.
// It matches the user portion of a user@host
func isValidUserMask(s string) bool {
	matched, err := regexp.MatchString("^[a-zA-Z0-9*?~]+$", s)
	if err != nil {
		return false
	}
	return matched
}

// Check if a string is a valid host mask.
// This is a pattern with * or ? glob style characters.
// It matches the host portion of a user@host
//
// TODO: Improve the host regex
func isValidHostMask(s string) bool {
	matched, err := regexp.MatchString("^[a-zA-Z0-9-.*?]+$", s)
	if err != nil {
		return false
	}
	return matched
}

func isNumericCommand(command string) bool {
	for _, c := range command {
		if c < 48 || c > 57 {
			return false
		}
	}
	return true
}

func isValidUID(s string) bool {
	// SID + ID = UID
	if len(s) != 9 {
		return false
	}

	if !isValidSID(s[0:3]) {
		return false
	}
	return isValidID(s[3:])
}

func isValidID(s string) bool {
	matched, err := regexp.MatchString("^[A-Z][A-Z0-9]{5}$", s)
	if err != nil {
		return false
	}
	return matched
}

func isValidSID(s string) bool {
	matched, err := regexp.MatchString("^[0-9][0-9A-Z]{2}$", s)
	if err != nil {
		return false
	}
	return matched
}

// Make TS6 ID. 6 characters long, [A-Z][A-Z0-9]{5}. Must be unique on this
// server.
// I already assign clients a unique integer ID per server. Use this to generate
// a TS6 ID.
// Take integer ID and convert it to base 36. (A-Z and 0-9)
func makeTS6ID(id uint64) (TS6ID, error) {
	// Check the integer ID is < 26*36**5. That is as many valid TS6 IDs we can
	// have. The first character must be [A-Z], the remaining 5 are [A-Z0-9],
	// hence 36**5 vs. 26.
	// This is also the maximum number of connections we can have per run.
	// 1,572,120,576
	if id >= 1572120576 {
		return "", fmt.Errorf("TS6 ID overflow")
	}

	n := id

	ts6id := []byte("AAAAAA")

	for pos := 5; pos >= 0; pos-- {
		if n >= 36 {
			rem := n % 36

			// 0 to 25 are A to Z
			// 26 to 35 are 0 to 9
			if rem >= 26 {
				ts6id[pos] = byte(rem - 26 + '0')
			} else {
				ts6id[pos] = byte(rem + 'A')
			}

			n /= 36
			continue
		}

		if n >= 26 {
			ts6id[pos] = byte(n - 26 + '0')
		} else {
			ts6id[pos] = byte(n + 'A')
		}

		// Once we are < 36, we're done.
		break
	}

	return TS6ID(ts6id), nil
}

// Convert a mask to a regexp.
// This quotes all regexp metachars, and then turns "*" into ".*", and "?"
// into ".".
func maskToRegex(mask string) (*regexp.Regexp, error) {
	regex := regexp.QuoteMeta(mask)
	regex = strings.Replace(regex, "\\*", ".*", -1)
	regex = strings.Replace(regex, "\\?", ".", -1)

	re, err := regexp.Compile(regex)
	if err != nil {
		return nil, err
	}

	return re, nil
}

var resolver = net.Resolver{
	PreferGo:     true,
	StrictErrors: true,
}

// Attempt to resolve a client's IP to a hostname.
//
// This is a forward confirmed DNS lookup.
//
// First we look up IP reverse DNS and find name(s).
//
// We then look up each of these name(s) and if one of them matches the IP,
// then we say the client has that host.
//
// If none match, we return blank indicating no hostname found.
func lookupHostname(ctx context.Context, ip net.IP) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	names, err := resolver.LookupAddr(ctx, ip.String())
	if err != nil {
		return ""
	}

	for _, name := range names {
		ips, err := resolver.LookupIPAddr(ctx, name)
		if err != nil {
			continue
		}

		for _, foundIP := range ips {
			if foundIP.IP.Equal(ip) {
				// Drop trailing "."
				return strings.TrimSuffix(name, ".")
			}
		}
	}

	return ""
}

func tlsVersionToString(version uint16) string {
	switch version {
	case tls.VersionSSL30:
		return "SSL 3.0"
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("Unknown TLS version %x", version)
	}
}

func cipherSuiteToString(suite uint16) string {
	switch suite {
	case tls.TLS_RSA_WITH_RC4_128_SHA:
		return "TLS_RSA_WITH_RC4_128_SHA"
	case tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA:
		return "TLS_RSA_WITH_3DES_EDE_CBC_SHA"
	case tls.TLS_RSA_WITH_AES_128_CBC_SHA:
		return "TLS_RSA_WITH_AES_128_CBC_SHA"
	case tls.TLS_RSA_WITH_AES_256_CBC_SHA:
		return "TLS_RSA_WITH_AES_256_CBC_SHA"
	case tls.TLS_RSA_WITH_AES_128_CBC_SHA256:
		return "TLS_RSA_WITH_AES_128_CBC_SHA256"
	case tls.TLS_RSA_WITH_AES_128_GCM_SHA256:
		return "TLS_RSA_WITH_AES_128_GCM_SHA256"
	case tls.TLS_RSA_WITH_AES_256_GCM_SHA384:
		return "TLS_RSA_WITH_AES_256_GCM_SHA384"
	case tls.TLS_ECDHE_ECDSA_WITH_RC4_128_SHA:
		return "TLS_ECDHE_ECDSA_WITH_RC4_128_SHA"
	case tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA:
		return "TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA"
	case tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA:
		return "TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA"
	case tls.TLS_ECDHE_RSA_WITH_RC4_128_SHA:
		return "TLS_ECDHE_RSA_WITH_RC4_128_SHA"
	case tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA:
		return "TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA"
	case tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA:
		return "TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA"
	case tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA:
		return "TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA"
	case tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256:
		return "TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256"
	case tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256:
		return "TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256"
	case tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256:
		return "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"
	case tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256:
		return "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256"
	case tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384:
		return "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"
	case tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384:
		return "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384"
	case tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305:
		return "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305"
	case tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305:
		return "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305"
	case tls.TLS_AES_128_GCM_SHA256:
		return "TLS_AES_128_GCM_SHA256"
	case tls.TLS_AES_256_GCM_SHA384:
		return "TLS_AES_256_GCM_SHA384"
	case tls.TLS_CHACHA20_POLY1305_SHA256:
		return "TLS_CHACHA20_POLY1305_SHA256"
	default:
		return fmt.Sprintf("Unknown cipher suite %x", suite)
	}
}

// Take a request set of user mode changes and parse them and apply the changes.
//
// We check each for whether it is valid to apply.
//
// Parameters:
// - modes - The requested mode change by the user. Unvalidated.
// - currentModes - The modes the user currently has.
//
// Returns:
// - Modes set.
// - Modes unset.
// - Unknown modes.
// - Whether there was an error (e.g., parsing error). If this is set, then
//   there will be no useful mode information returned.
func parseAndResolveUmodeChanges(modes string,
	currentModes map[byte]struct{}) (map[byte]struct{}, map[byte]struct{},
	map[byte]struct{}, error) {
	// Requested mode changes. We don't know they will be applied.
	requestSetModes := make(map[byte]struct{})
	requestUnsetModes := make(map[byte]struct{})

	// + / -. Apparently +/- is optional. If we don't see +/- before a mode then
	// assume +.
	action := '+'

	// Parse the mode string. Find those requested to be set and unset.
	for _, char := range modes {
		if char == '+' || char == '-' {
			action = char
			continue
		}

		if action == '+' {
			requestSetModes[byte(char)] = struct{}{}
			continue
		}

		if action == '-' {
			requestUnsetModes[byte(char)] = struct{}{}
			continue
		}
	}

	// Filter out modes we don't support. Track them too.
	unknownModes := make(map[byte]struct{})

	for mode := range requestSetModes {
		if mode != 'i' && mode != 'o' && mode != 'C' {
			delete(requestSetModes, mode)
			unknownModes[mode] = struct{}{}
		}
	}
	for mode := range requestUnsetModes {
		if mode != 'i' && mode != 'o' && mode != 'C' {
			delete(requestUnsetModes, mode)
			unknownModes[mode] = struct{}{}
		}
	}

	// Unsetting certain modes triggers unsetting others. They're dependent.
	for mode := range requestUnsetModes {
		if mode == 'o' {
			// Must be operator to have +C.
			requestUnsetModes['C'] = struct{}{}
			// Block any request to set it.
			_, exists := requestSetModes['C']
			if exists {
				delete(requestSetModes, 'C')
			}
		}
	}

	// If any modes are to be both set and unset, forget them. Ambiguous.
	for mode := range requestSetModes {
		_, exists := requestUnsetModes[mode]
		if exists {
			delete(requestSetModes, mode)
			delete(requestUnsetModes, mode)
		}
	}

	// Apply changes. Only if applying them makes sense and is legal.

	// Track changes made.
	setModes := make(map[byte]struct{})
	unsetModes := make(map[byte]struct{})

	for mode := range requestUnsetModes {
		// Don't have it? Nothing to change.
		_, exists := currentModes[mode]
		if !exists {
			continue
		}

		// Unset it.
		unsetModes[mode] = struct{}{}
		delete(currentModes, mode)
	}

	for mode := range requestSetModes {
		// Have it already? Nothing to change.
		_, exists := currentModes[mode]
		if exists {
			continue
		}

		// Ignore it if they try to +o (operator) themselves. (RFC says to do so,
		// but it only comes from OPER).
		if mode == 'o' {
			continue
		}

		// Must be +o to have +C.
		if mode == 'C' {
			_, exists := currentModes['o']
			if exists {
				currentModes[mode] = struct{}{}
				setModes[mode] = struct{}{}
			}
		}

		if mode == 'i' {
			currentModes[mode] = struct{}{}
			setModes[mode] = struct{}{}
			continue
		}
	}

	return setModes, unsetModes, unknownModes, nil
}

// Certain commands accept a parameter that is a comma separated list of
// channels. e.g. JOIN #one,#two means to join #one and #two.
// This function parses such a parameter into its parts.
//
// It returns canonicalized channel names, and only those which are valid. It
// also drops any duplicates.
func commaChannelsToChannelNames(s string) []string {
	channelNames := make(map[string]struct{})

	rawChannelNames := strings.Split(s, ",")

	for _, rawChannelName := range rawChannelNames {
		rawChannelName = strings.TrimSpace(rawChannelName)

		if len(rawChannelName) == 0 {
			continue
		}

		rawChannelName = canonicalizeChannel(rawChannelName)

		if !isValidChannel(rawChannelName) {
			continue
		}

		channelNames[rawChannelName] = struct{}{}
	}

	channelNameList := []string{}
	for channelName := range channelNames {
		channelNameList = append(channelNameList, channelName)
	}

	return channelNameList
}

// Take a space separated capabilities string and return a map.
func parseCapabsString(s string) map[string]struct{} {
	rawCapabs := strings.Split(s, " ")
	capabs := make(map[string]struct{})

	for _, cap := range rawCapabs {
		cap = strings.TrimSpace(cap)
		if len(cap) == 0 {
			continue
		}

		cap = strings.ToUpper(cap)

		capabs[cap] = struct{}{}
	}

	return capabs
}

// Sort server maps by hop count, ascending.
func sortServersByHopCount(serverMap map[TS6SID]*Server) []*Server {
	servers := []*Server{}

	for _, server := range serverMap {
		servers = append(servers, server)
	}

	sort.Sort(ByHopCount(servers))

	return servers
}

// Create a line for a response in the map command output.
// Refer to mapCommand() for details.
func serverToMapLine(name string, sid TS6SID, localUsers, globalUsers,
	hopCount int) string {
	// Each line is the same length.
	lineLen := 74

	// For each hop, add two spaces to indicate it's subordinate.
	serverName := ""
	for i := 0; i < hopCount; i++ {
		serverName += "  "
	}

	// irc.example.com[000]
	serverName += fmt.Sprintf("%s[%s] ", name, string(sid))

	// Determine how wide the user count portion should be.
	// To do that, assume each server has the globalUser count (though this
	// will usually not be the case). Then use that width.
	globalCountString := fmt.Sprintf("%d", globalUsers)
	userCountLenString := fmt.Sprintf("%d", len(globalCountString))

	// Determine percentage of global users are on this server.
	percent := float64(localUsers) / float64(globalUsers) * 100.0

	// | Users: n (100.0%)
	users := fmt.Sprintf(" | Users: %"+userCountLenString+"d (%5.1f%%)",
		localUsers, percent)

	// Pad dashes in between server name and user count.
	numDashes := lineLen - len(serverName) - len(users)
	dashes := ""
	for i := 0; i < numDashes; i++ {
		dashes += "-"
	}

	// irc.example.com[000] ---------- | Users: n (100.0%)
	return serverName + dashes + users
}

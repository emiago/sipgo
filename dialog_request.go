package sipgo

import (
	"fmt"
	"strings"

	"github.com/emiago/sipgo/sip"
)

// DialogRequestOption configures one outgoing dialog request.
type DialogRequestOption struct {
	transport string
}

// WithDialogRequestTransport selects the transport for one request, without changing the dialog.
// The caller must preserve the route set, destination port and URI transport requirements.
// Nonsecure transports are rejected for SIPS dialogs and next hops.
func WithDialogRequestTransport(transport string) DialogRequestOption {
	return DialogRequestOption{transport: strings.ToUpper(transport)}
}

func validateDialogRequestOptions(invite *sip.Request, answer *sip.Response, req *sip.Request, options []DialogRequestOption) error {
	if len(options) == 0 {
		return nil
	}
	secure, err := dialogRequiresSIPS(invite, answer, req)
	if err != nil {
		return err
	}
	for _, option := range options {
		switch option.transport {
		case "UDP", "TCP", "TLS", "WS", "WSS":
		default:
			return fmt.Errorf("unsupported dialog request transport %q", option.transport)
		}
		if secure && option.transport != "TLS" && option.transport != "WSS" {
			return fmt.Errorf("SIPS requires a secure dialog request transport, got %s", option.transport)
		}
	}
	return nil
}

// dialogRequiresSIPS reports whether the dialog, target, contacts or any route URI uses SIPS.
func dialogRequiresSIPS(invite *sip.Request, answer *sip.Response, req *sip.Request) (bool, error) {
	if isSIPS(invite.Recipient) || isSIPS(req.Recipient) {
		return true, nil
	}
	if contact := invite.Contact(); contact != nil && isSIPS(contact.Address) {
		return true, nil
	}
	routes := append([]sip.Header(nil), invite.GetHeaders("Record-Route")...)
	routes = append(routes, req.GetHeaders("Route")...)
	if answer != nil {
		if contact := answer.Contact(); contact != nil && isSIPS(contact.Address) {
			return true, nil
		}
		routes = append(routes, answer.GetHeaders("Record-Route")...)
	}
	parser := sip.HeadersParser(sip.DefaultHeadersParser())
	for _, h := range routes {
		// Parse every URI, including combined Record-Route values.
		parsed, err := parser.ParseHeader(nil, []byte("Route: "+h.Value()))
		if err != nil {
			return false, fmt.Errorf("parse dialog route: %w", err)
		}
		for _, route := range parsed {
			if r, ok := route.(*sip.RouteHeader); ok && isSIPS(r.Address) {
				return true, nil
			}
		}
	}
	return false, nil
}

func isSIPS(uri sip.Uri) bool {
	return strings.EqualFold(uri.Scheme, "sips")
}

// applyDialogRequestOptions sets the selected transport and restores the caller's destination.
func applyDialogRequestOptions(req *sip.Request, destination string, options []DialogRequestOption) {
	if len(options) == 0 {
		return
	}
	req.SetTransport(options[len(options)-1].transport)
	// Destinations derived from the INVITE source belong to the original transport.
	req.SetDestination(destination)
	// The client builds a fresh Via for the selected transport.
	for req.Via() != nil {
		req.RemoveHeader("Via")
	}
	req.Laddr = sip.Addr{}
}

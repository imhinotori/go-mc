package command

import (
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

type Client interface {
	SendPacket(p pk.Packet)
}

// ClientJoin implement server.Component for Graph
func (g *Graph) ClientJoin(client Client) {
	client.SendPacket(pk.Marshal(
		packetid.ClientboundCommands, g,
	))
}

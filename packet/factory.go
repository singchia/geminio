package packet

import "github.com/singchia/geminio/pkg/id"

type PacketFactory struct {
	packetIDs id.IDFactory
}

func NewPacketFactory(packetIDs *id.IDCounter) *PacketFactory {
	return &PacketFactory{packetIDs}
}

// connection layer packets
func (pf *PacketFactory) NewConnPacket(wantedClientID uint64, clientIDPeersCall bool,
	heartbeat Heartbeat, meta []byte) *ConnPacket {
	packetID := pf.packetIDs.GetID()
	connPkt := &ConnPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeConnPacket,
			PacketID: packetID,
			Cnss:     CnssAtMostOnce,
		},
		ConnFlags: ConnFlags{
			Heartbeat: heartbeat,
		},
		ClientID: wantedClientID,
		ConnData: &ConnData{
			Meta: meta,
		},
	}
	connPkt.clientIDAcquire = clientIDPeersCall
	return connPkt
}

// 协商的ClientID
func (pf *PacketFactory) NewConnAckPacket(packetID uint64,
	confirmedClientID uint64, err error) *ConnAckPacket {
	connAckPkt := &ConnAckPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeConnAckPacket,
			PacketID: packetID,
			Cnss:     CnssAtMostOnce,
		},
		RetCode:  RetCodeOK,
		ClientID: confirmedClientID,
		ConnData: &ConnData{},
	}
	if err != nil {
		connAckPkt.RetCode = RetCodeERR
		connAckPkt.ConnData.Error = err.Error()
	}
	return connAckPkt
}

func (pf *PacketFactory) NewDisConnPacket() *DisConnPacket {
	packetID := pf.packetIDs.GetID()
	disConnPkt := &DisConnPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeDisConnPacket,
			PacketID: packetID,
			Cnss:     CnssAtMostOnce,
		},
	}
	return disConnPkt
}

func (pf *PacketFactory) NewDisConnAckPacket(packetID uint64,
	err error) *DisConnAckPacket {
	disConnAckPkt := &DisConnAckPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeDisConnAckPacket,
			PacketID: packetID,
			Cnss:     CnssAtMostOnce,
		},
		RetCode:  RetCodeOK,
		ConnData: &ConnData{},
	}
	if err != nil {
		disConnAckPkt.RetCode = RetCodeERR
		disConnAckPkt.ConnData.Error = err.Error()
	}
	return disConnAckPkt
}

func (pf *PacketFactory) NewHeartbeatPacket() *HeartbeatPacket {
	packetID := pf.packetIDs.GetID()
	hbPkt := &HeartbeatPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeHeartbeatPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
	}
	return hbPkt
}

func (pf *PacketFactory) NewHeartbeatAckPacket(packetID uint64) *HeartbeatAckPacket {
	hbAckPkt := &HeartbeatAckPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeHeartbeatAckPacket,
			PacketID: packetID,
			Cnss:     CnssAtMostOnce,
		},
	}
	return hbAckPkt
}

// session layer packets
func (pf *PacketFactory) NewSessionPacket(negotiateID uint64, sessionIDPeersCall bool, meta []byte) *SessionPacket {
	packetID := pf.packetIDs.GetID()
	snPkt := &SessionPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeSessionPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		SessionFlags: SessionFlags{
			sessionIDAcquire: sessionIDPeersCall,
		},
		negotiateID: negotiateID,
		SessionData: &SessionData{
			Meta: meta,
		},
	}
	return snPkt
}

func (pf *PacketFactory) NewSessionAckPacket(packetID uint64, negotiateID uint64,
	confirmedSessionID uint64, err error) *SessionAckPacket {
	snAckPkt := &SessionAckPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeSessionAckPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		negotiateID: negotiateID,
		sessionID:   confirmedSessionID,
		SessionData: &SessionData{},
	}
	if err != nil {
		snAckPkt.SessionData.Error = err.Error()
	}
	return snAckPkt
}

func (pf *PacketFactory) NewDismissPacket(sessionID uint64) *DismissPacket {
	packetID := pf.packetIDs.GetID()
	disPkt := &DismissPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeDismissPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		sessionID:   sessionID,
		SessionData: &SessionData{},
	}
	return disPkt
}

func (pf *PacketFactory) NewDismissAckPacket(packetID uint64,
	sessionID uint64, err error) *DismissAckPacket {
	disAckPkt := &DismissAckPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeDismissAckPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		sessionID:   sessionID,
		SessionData: &SessionData{},
	}
	if err != nil {
		disAckPkt.SessionData.Error = err.Error()
	}
	return disAckPkt
}

// application layer packets
func (pf *PacketFactory) NewMessagePacket(key, value, custom []byte) *MessagePacket {
	packetID := pf.packetIDs.GetID()
	msgPkt := &MessagePacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeMessagePacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		MessageData: &MessageData{
			Key:    key,
			Value:  value,
			Custom: custom,
		},
	}
	return msgPkt
}

func (pf *PacketFactory) NewMessagePacketWithSessionID(sessionID uint64,
	key, value, custom []byte) *MessagePacket {
	packetID := pf.packetIDs.GetID()
	msgPkt := &MessagePacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeMessagePacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		sessionID: sessionID,
		MessageData: &MessageData{
			Key:    key,
			Value:  value,
			Custom: custom,
		},
	}
	return msgPkt
}

func (pf *PacketFactory) NewMessageAckPacket(packetID uint64, err error) *MessageAckPacket {
	msgAckPkt := &MessageAckPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeMessageAckPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		MessageData: &MessageData{},
	}
	if err != nil {
		msgAckPkt.MessageData.Error = err.Error()
	}
	return msgAckPkt
}

func (pf *PacketFactory) NewRequestPacket(pattern, data, custom []byte) *RequestPacket {
	packetID := pf.packetIDs.GetID()
	msgPkt := &MessagePacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeRequestPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		MessageData: &MessageData{
			Key:    pattern,
			Value:  data,
			Custom: custom,
		},
	}
	return &RequestPacket{msgPkt}
}

func (pf *PacketFactory) NewResponsePacket(requestPacketID uint64,
	pattern, data, custom []byte, err error) *ResponsePacket {
	msgAckPkt := &MessageAckPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeResponsePacket,
			PacketID: requestPacketID,
			Cnss:     CnssAtLeastOnce,
		},
		MessageData: &MessageData{
			Key:    pattern,
			Value:  data,
			Custom: custom,
		},
	}
	if err != nil {
		msgAckPkt.MessageData.Error = err.Error()
	}
	return &ResponsePacket{msgAckPkt}
}

func (pf *PacketFactory) NewStreamPacket(data []byte) *StreamPacket {
	packetID := pf.packetIDs.GetID()
	streamPkt := &StreamPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeStreamPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		Data: data,
	}
	return streamPkt
}

func (pf *PacketFactory) NewRegisterPacket(method []byte) *RegisterPacket {
	packetID := pf.packetIDs.GetID()
	registerPkt := &RegisterPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeRegisterPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		Method: method,
	}
	return registerPkt
}

func (pf *PacketFactory) NewRegisterAckPacket(packetID uint64, err error) *RegisterAckPacket {
	registerAckPkt := &RegisterAckPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeRegisterAckPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		RegisterData: &RegisterData{},
	}
	if err != nil {
		registerAckPkt.RegisterData.Error = err.Error()
	}
	return registerAckPkt
}

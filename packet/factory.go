package packet

import "github.com/singchia/geminio/pkg/id"

type PacketFactory interface {
	NewPacketID() uint64
	// conn layer
	NewConnPacket(wanted uint64, peersCall bool, heartbeat Heartbeat, meta []byte) *ConnPacket
	NewConnAckPacket(packetID uint64, confirmedClientID uint64, err error) *ConnAckPacket
	NewDisConnPacket() *DisConnPacket
	NewDisConnAckPacket(packetID uint64, err error) *DisConnAckPacket
	NewHeartbeatPacket() *HeartbeatPacket
	NewHeartbeatAckPacket(packetID uint64) *HeartbeatAckPacket
	// session layer
	NewSessionPacket(negotiateID uint64, sessionIDPeersCall bool, meta []byte, peer string) *SessionPacket
	NewSessionAckPacket(packetID uint64, negotiateID uint64, confirmedID uint64, err error) *SessionAckPacket
	NewDismissPacket(sessionID uint64) *DismissPacket
	NewDismissAckPacket(packetID uint64, sessionID uint64, err error) *DismissAckPacket
	// application layer
	NewMessagePacket(key, value []byte) *MessagePacket
	NewMessagePacketWithIDAndSessionID(id, sessionID uint64, key, value []byte) *MessagePacket
	NewMessagePacketWithSessionID(sessionID uint64, key, value, custom []byte) *MessagePacket
	NewMessageAckPacket(packetID uint64, err error) *MessageAckPacket
	NewMessageAckPacketWithSessionID(sessionID, packetID uint64, err error) *MessageAckPacket
	NewRequestPacket(pattern, data []byte) *RequestPacket
	NewRequestCancelPacketWithIDAndSessionID(id, sessionID uint64, cancelType RequestCancelType) *RequestCancelPacket
	NewRequestPacketWithIDAndSessionID(id, sessionID uint64, pattern, data []byte) *RequestPacket
	NewResponsePacket(requestPacketID uint64, pattern, data []byte, err error) *ResponsePacket
	NewStreamPacket(data []byte) *StreamPacket
	NewStreamPacketWithSessionID(sessionID uint64, data []byte) *StreamPacket
	NewRegisterPacket(method []byte) *RegisterPacket
	NewRegisterPacketWithSessionID(sessionID uint64, method []byte) *RegisterPacket
	NewRegisterAckPacket(packetID uint64, err error) *RegisterAckPacket
	NewRegisterAckPacketWithSessionID(sessionID uint64, packetID uint64, err error) *RegisterAckPacket
}

type packetFactory struct {
	packetIDs id.IDFactory
}

func NewPacketFactory(packetIDs *id.IDCounter) PacketFactory {
	return &packetFactory{packetIDs}
}

func (pf *packetFactory) NewPacketID() uint64 {
	return pf.packetIDs.GetID()
}

// connection layer packets
func (pf *packetFactory) NewConnPacket(wantedClientID uint64, clientIDPeersCall bool,
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
func (pf *packetFactory) NewConnAckPacket(packetID uint64,
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

func (pf *packetFactory) NewDisConnPacket() *DisConnPacket {
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

func (pf *packetFactory) NewDisConnAckPacket(packetID uint64,
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

func (pf *packetFactory) NewHeartbeatPacket() *HeartbeatPacket {
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

func (pf *packetFactory) NewHeartbeatAckPacket(packetID uint64) *HeartbeatAckPacket {
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
func (pf *packetFactory) NewSessionPacket(negotiateID uint64, sessionIDPeersCall bool, meta []byte, peer string) *SessionPacket {
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
			Peer: peer,
		},
	}
	return snPkt
}

func (pf *packetFactory) NewSessionAckPacket(packetID uint64, negotiateID uint64,
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

func (pf *packetFactory) NewDismissPacket(sessionID uint64) *DismissPacket {
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

func (pf *packetFactory) NewDismissAckPacket(packetID uint64,
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
func (pf *packetFactory) NewMessagePacket(key, value []byte) *MessagePacket {
	packetID := pf.packetIDs.GetID()
	msgPkt := &MessagePacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeMessagePacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		Data: &MessageData{
			Key:   key,
			Value: value,
			//Custom: custom,
		},
	}
	return msgPkt
}

func (pf *packetFactory) NewMessagePacketWithIDAndSessionID(id, sessionID uint64, key, value []byte) *MessagePacket {
	pkt := pf.NewMessagePacket(key, value)
	pkt.PacketID = id
	pkt.sessionID = sessionID
	return pkt
}

func (pf *packetFactory) NewMessagePacketWithSessionID(sessionID uint64,
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
		Data: &MessageData{
			Key:   key,
			Value: value,
			//Custom: custom,
		},
	}
	return msgPkt
}

func (pf *packetFactory) NewMessageAckPacket(packetID uint64, err error) *MessageAckPacket {
	msgAckPkt := &MessageAckPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeMessageAckPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		Data: &MessageData{},
	}
	if err != nil {
		msgAckPkt.Data.Error = err.Error()
	}
	return msgAckPkt
}

func (pf *packetFactory) NewMessageAckPacketWithSessionID(sessionID, packetID uint64, err error) *MessageAckPacket {
	pkt := pf.NewMessageAckPacket(packetID, err)
	pkt.sessionID = sessionID
	return pkt
}

func (pf *packetFactory) NewRequestPacket(pattern, data []byte) *RequestPacket {
	packetID := pf.packetIDs.GetID()
	msgPkt := &MessagePacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeRequestPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		Data: &MessageData{
			Key:   pattern,
			Value: data,
		},
	}
	return &RequestPacket{msgPkt}
}

func (pf *packetFactory) NewRequestCancelPacketWithIDAndSessionID(id, sessionID uint64, cancelType RequestCancelType) *RequestCancelPacket {
	reqCelPkt := &RequestCancelPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeRequestCancelPacket,
			PacketID: id,
			Cnss:     CnssAtLeastOnce,
		},
		sessionID:  sessionID,
		cancelType: cancelType,
	}
	return reqCelPkt
}

func (pf *packetFactory) NewRequestPacketWithIDAndSessionID(id, sessionID uint64, pattern, data []byte) *RequestPacket {
	pkt := pf.NewRequestPacket(pattern, data)
	pkt.PacketID = id
	pkt.sessionID = sessionID
	return pkt
}

func (pf *packetFactory) NewResponsePacket(requestPacketID uint64,
	pattern, data []byte, err error) *ResponsePacket {
	msgAckPkt := &MessageAckPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeResponsePacket,
			PacketID: requestPacketID,
			Cnss:     CnssAtLeastOnce,
		},
		Data: &MessageData{
			Key:   pattern,
			Value: data,
		},
	}
	if err != nil {
		msgAckPkt.Data.Error = err.Error()
	}
	return &ResponsePacket{msgAckPkt}
}

func (pf *packetFactory) NewStreamPacket(data []byte) *StreamPacket {
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

func (pf *packetFactory) NewStreamPacketWithSessionID(sessionID uint64, data []byte) *StreamPacket {
	pkt := pf.NewStreamPacket(data)
	pkt.sessionID = sessionID
	return pkt
}

func (pf *packetFactory) NewRegisterPacket(method []byte) *RegisterPacket {
	packetID := pf.packetIDs.GetID()
	registerPkt := &RegisterPacket{
		PacketHeader: &PacketHeader{
			Version:  V01,
			Typ:      TypeRegisterPacket,
			PacketID: packetID,
			Cnss:     CnssAtLeastOnce,
		},
		method: method,
	}
	return registerPkt
}

func (pf *packetFactory) NewRegisterPacketWithSessionID(sessionID uint64, method []byte) *RegisterPacket {
	pkt := pf.NewRegisterPacket(method)
	pkt.sessionID = sessionID
	return pkt
}

func (pf *packetFactory) NewRegisterAckPacket(packetID uint64, err error) *RegisterAckPacket {
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

func (pf *packetFactory) NewRegisterAckPacketWithSessionID(sessionID uint64, packetID uint64, err error) *RegisterAckPacket {
	pkt := pf.NewRegisterAckPacket(packetID, err)
	pkt.sessionID = sessionID
	return pkt
}

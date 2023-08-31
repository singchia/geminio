// Code generated by MockGen. DO NOT EDIT.
// Source: ../multiplexer/multiplexer.go

// Package mock_multiplexer is a generated GoMock package.
package mock

import (
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
	geminio "github.com/singchia/geminio"
	multiplexer "github.com/singchia/geminio/multiplexer"
	packet "github.com/singchia/geminio/packet"
)

// MockMultiplexer is a mock of Multiplexer interface.
type MockMultiplexer struct {
	ctrl     *gomock.Controller
	recorder *MockMultiplexerMockRecorder
}

// MockMultiplexerMockRecorder is the mock recorder for MockMultiplexer.
type MockMultiplexerMockRecorder struct {
	mock *MockMultiplexer
}

// NewMockMultiplexer creates a new mock instance.
func NewMockMultiplexer(ctrl *gomock.Controller) *MockMultiplexer {
	mock := &MockMultiplexer{ctrl: ctrl}
	mock.recorder = &MockMultiplexerMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockMultiplexer) EXPECT() *MockMultiplexerMockRecorder {
	return m.recorder
}

// AcceptDialogue mocks base method.
func (m *MockMultiplexer) AcceptDialogue() (multiplexer.Dialogue, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AcceptDialogue")
	ret0, _ := ret[0].(multiplexer.Dialogue)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// AcceptDialogue indicates an expected call of AcceptDialogue.
func (mr *MockMultiplexerMockRecorder) AcceptDialogue() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AcceptDialogue", reflect.TypeOf((*MockMultiplexer)(nil).AcceptDialogue))
}

// Close mocks base method.
func (m *MockMultiplexer) Close() {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "Close")
}

// Close indicates an expected call of Close.
func (mr *MockMultiplexerMockRecorder) Close() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Close", reflect.TypeOf((*MockMultiplexer)(nil).Close))
}

// ClosedDialogue mocks base method.
func (m *MockMultiplexer) ClosedDialogue() (multiplexer.Dialogue, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ClosedDialogue")
	ret0, _ := ret[0].(multiplexer.Dialogue)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ClosedDialogue indicates an expected call of ClosedDialogue.
func (mr *MockMultiplexerMockRecorder) ClosedDialogue() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ClosedDialogue", reflect.TypeOf((*MockMultiplexer)(nil).ClosedDialogue))
}

// GetDialogue mocks base method.
func (m *MockMultiplexer) GetDialogue(clientID, dialogueID uint64) (multiplexer.Dialogue, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetDialogue", clientID, dialogueID)
	ret0, _ := ret[0].(multiplexer.Dialogue)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetDialogue indicates an expected call of GetDialogue.
func (mr *MockMultiplexerMockRecorder) GetDialogue(clientID, dialogueID interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetDialogue", reflect.TypeOf((*MockMultiplexer)(nil).GetDialogue), clientID, dialogueID)
}

// ListDialogues mocks base method.
func (m *MockMultiplexer) ListDialogues() []multiplexer.Dialogue {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListDialogues")
	ret0, _ := ret[0].([]multiplexer.Dialogue)
	return ret0
}

// ListDialogues indicates an expected call of ListDialogues.
func (mr *MockMultiplexerMockRecorder) ListDialogues() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListDialogues", reflect.TypeOf((*MockMultiplexer)(nil).ListDialogues))
}

// OpenDialogue mocks base method.
func (m *MockMultiplexer) OpenDialogue(meta []byte) (multiplexer.Dialogue, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "OpenDialogue", meta)
	ret0, _ := ret[0].(multiplexer.Dialogue)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// OpenDialogue indicates an expected call of OpenDialogue.
func (mr *MockMultiplexerMockRecorder) OpenDialogue(meta interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "OpenDialogue", reflect.TypeOf((*MockMultiplexer)(nil).OpenDialogue), meta)
}

// MockReader is a mock of Reader interface.
type MockReader struct {
	ctrl     *gomock.Controller
	recorder *MockReaderMockRecorder
}

// MockReaderMockRecorder is the mock recorder for MockReader.
type MockReaderMockRecorder struct {
	mock *MockReader
}

// NewMockReader creates a new mock instance.
func NewMockReader(ctrl *gomock.Controller) *MockReader {
	mock := &MockReader{ctrl: ctrl}
	mock.recorder = &MockReaderMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockReader) EXPECT() *MockReaderMockRecorder {
	return m.recorder
}

// Read mocks base method.
func (m *MockReader) Read() (packet.Packet, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Read")
	ret0, _ := ret[0].(packet.Packet)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Read indicates an expected call of Read.
func (mr *MockReaderMockRecorder) Read() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Read", reflect.TypeOf((*MockReader)(nil).Read))
}

// ReadC mocks base method.
func (m *MockReader) ReadC() <-chan packet.Packet {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ReadC")
	ret0, _ := ret[0].(<-chan packet.Packet)
	return ret0
}

// ReadC indicates an expected call of ReadC.
func (mr *MockReaderMockRecorder) ReadC() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ReadC", reflect.TypeOf((*MockReader)(nil).ReadC))
}

// MockWriter is a mock of Writer interface.
type MockWriter struct {
	ctrl     *gomock.Controller
	recorder *MockWriterMockRecorder
}

// MockWriterMockRecorder is the mock recorder for MockWriter.
type MockWriterMockRecorder struct {
	mock *MockWriter
}

// NewMockWriter creates a new mock instance.
func NewMockWriter(ctrl *gomock.Controller) *MockWriter {
	mock := &MockWriter{ctrl: ctrl}
	mock.recorder = &MockWriterMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockWriter) EXPECT() *MockWriterMockRecorder {
	return m.recorder
}

// Write mocks base method.
func (m *MockWriter) Write(pkt packet.Packet) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Write", pkt)
	ret0, _ := ret[0].(error)
	return ret0
}

// Write indicates an expected call of Write.
func (mr *MockWriterMockRecorder) Write(pkt interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Write", reflect.TypeOf((*MockWriter)(nil).Write), pkt)
}

// MockCloser is a mock of Closer interface.
type MockCloser struct {
	ctrl     *gomock.Controller
	recorder *MockCloserMockRecorder
}

// MockCloserMockRecorder is the mock recorder for MockCloser.
type MockCloserMockRecorder struct {
	mock *MockCloser
}

// NewMockCloser creates a new mock instance.
func NewMockCloser(ctrl *gomock.Controller) *MockCloser {
	mock := &MockCloser{ctrl: ctrl}
	mock.recorder = &MockCloserMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockCloser) EXPECT() *MockCloserMockRecorder {
	return m.recorder
}

// Close mocks base method.
func (m *MockCloser) Close() {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "Close")
}

// Close indicates an expected call of Close.
func (mr *MockCloserMockRecorder) Close() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Close", reflect.TypeOf((*MockCloser)(nil).Close))
}

// MockDialogueDescriber is a mock of DialogueDescriber interface.
type MockDialogueDescriber struct {
	ctrl     *gomock.Controller
	recorder *MockDialogueDescriberMockRecorder
}

// MockDialogueDescriberMockRecorder is the mock recorder for MockDialogueDescriber.
type MockDialogueDescriberMockRecorder struct {
	mock *MockDialogueDescriber
}

// NewMockDialogueDescriber creates a new mock instance.
func NewMockDialogueDescriber(ctrl *gomock.Controller) *MockDialogueDescriber {
	mock := &MockDialogueDescriber{ctrl: ctrl}
	mock.recorder = &MockDialogueDescriberMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockDialogueDescriber) EXPECT() *MockDialogueDescriberMockRecorder {
	return m.recorder
}

// ClientID mocks base method.
func (m *MockDialogueDescriber) ClientID() uint64 {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ClientID")
	ret0, _ := ret[0].(uint64)
	return ret0
}

// ClientID indicates an expected call of ClientID.
func (mr *MockDialogueDescriberMockRecorder) ClientID() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ClientID", reflect.TypeOf((*MockDialogueDescriber)(nil).ClientID))
}

// DialogueID mocks base method.
func (m *MockDialogueDescriber) DialogueID() uint64 {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DialogueID")
	ret0, _ := ret[0].(uint64)
	return ret0
}

// DialogueID indicates an expected call of DialogueID.
func (mr *MockDialogueDescriberMockRecorder) DialogueID() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DialogueID", reflect.TypeOf((*MockDialogueDescriber)(nil).DialogueID))
}

// Meta mocks base method.
func (m *MockDialogueDescriber) Meta() []byte {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Meta")
	ret0, _ := ret[0].([]byte)
	return ret0
}

// Meta indicates an expected call of Meta.
func (mr *MockDialogueDescriberMockRecorder) Meta() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Meta", reflect.TypeOf((*MockDialogueDescriber)(nil).Meta))
}

// NegotiatingID mocks base method.
func (m *MockDialogueDescriber) NegotiatingID() uint64 {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "NegotiatingID")
	ret0, _ := ret[0].(uint64)
	return ret0
}

// NegotiatingID indicates an expected call of NegotiatingID.
func (mr *MockDialogueDescriberMockRecorder) NegotiatingID() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "NegotiatingID", reflect.TypeOf((*MockDialogueDescriber)(nil).NegotiatingID))
}

// Side mocks base method.
func (m *MockDialogueDescriber) Side() geminio.Side {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Side")
	ret0, _ := ret[0].(geminio.Side)
	return ret0
}

// Side indicates an expected call of Side.
func (mr *MockDialogueDescriberMockRecorder) Side() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Side", reflect.TypeOf((*MockDialogueDescriber)(nil).Side))
}

// MockDialogue is a mock of Dialogue interface.
type MockDialogue struct {
	ctrl     *gomock.Controller
	recorder *MockDialogueMockRecorder
}

// MockDialogueMockRecorder is the mock recorder for MockDialogue.
type MockDialogueMockRecorder struct {
	mock *MockDialogue
}

// NewMockDialogue creates a new mock instance.
func NewMockDialogue(ctrl *gomock.Controller) *MockDialogue {
	mock := &MockDialogue{ctrl: ctrl}
	mock.recorder = &MockDialogueMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockDialogue) EXPECT() *MockDialogueMockRecorder {
	return m.recorder
}

// ClientID mocks base method.
func (m *MockDialogue) ClientID() uint64 {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ClientID")
	ret0, _ := ret[0].(uint64)
	return ret0
}

// ClientID indicates an expected call of ClientID.
func (mr *MockDialogueMockRecorder) ClientID() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ClientID", reflect.TypeOf((*MockDialogue)(nil).ClientID))
}

// Close mocks base method.
func (m *MockDialogue) Close() {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "Close")
}

// Close indicates an expected call of Close.
func (mr *MockDialogueMockRecorder) Close() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Close", reflect.TypeOf((*MockDialogue)(nil).Close))
}

// DialogueID mocks base method.
func (m *MockDialogue) DialogueID() uint64 {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DialogueID")
	ret0, _ := ret[0].(uint64)
	return ret0
}

// DialogueID indicates an expected call of DialogueID.
func (mr *MockDialogueMockRecorder) DialogueID() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DialogueID", reflect.TypeOf((*MockDialogue)(nil).DialogueID))
}

// Meta mocks base method.
func (m *MockDialogue) Meta() []byte {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Meta")
	ret0, _ := ret[0].([]byte)
	return ret0
}

// Meta indicates an expected call of Meta.
func (mr *MockDialogueMockRecorder) Meta() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Meta", reflect.TypeOf((*MockDialogue)(nil).Meta))
}

// NegotiatingID mocks base method.
func (m *MockDialogue) NegotiatingID() uint64 {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "NegotiatingID")
	ret0, _ := ret[0].(uint64)
	return ret0
}

// NegotiatingID indicates an expected call of NegotiatingID.
func (mr *MockDialogueMockRecorder) NegotiatingID() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "NegotiatingID", reflect.TypeOf((*MockDialogue)(nil).NegotiatingID))
}

// Read mocks base method.
func (m *MockDialogue) Read() (packet.Packet, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Read")
	ret0, _ := ret[0].(packet.Packet)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Read indicates an expected call of Read.
func (mr *MockDialogueMockRecorder) Read() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Read", reflect.TypeOf((*MockDialogue)(nil).Read))
}

// ReadC mocks base method.
func (m *MockDialogue) ReadC() <-chan packet.Packet {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ReadC")
	ret0, _ := ret[0].(<-chan packet.Packet)
	return ret0
}

// ReadC indicates an expected call of ReadC.
func (mr *MockDialogueMockRecorder) ReadC() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ReadC", reflect.TypeOf((*MockDialogue)(nil).ReadC))
}

// Side mocks base method.
func (m *MockDialogue) Side() geminio.Side {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Side")
	ret0, _ := ret[0].(geminio.Side)
	return ret0
}

// Side indicates an expected call of Side.
func (mr *MockDialogueMockRecorder) Side() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Side", reflect.TypeOf((*MockDialogue)(nil).Side))
}

// Write mocks base method.
func (m *MockDialogue) Write(pkt packet.Packet) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Write", pkt)
	ret0, _ := ret[0].(error)
	return ret0
}

// Write indicates an expected call of Write.
func (mr *MockDialogueMockRecorder) Write(pkt interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Write", reflect.TypeOf((*MockDialogue)(nil).Write), pkt)
}
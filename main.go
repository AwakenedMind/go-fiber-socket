package fibersocket

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
)

type Event struct {
	// Message type
	mType int
	// Message data
	data []byte
	// Message send retries when error
	retries int
}

type FiberSocket struct {
	Mutex             sync.RWMutex
	ws                *websocket.Conn
	IsConnectionAlive bool
	Done              chan struct{}
	EventQueue        chan Event
	EventAttributes   map[string]interface{}
	UUID              uuid.UUID
	Locals            func(key string) interface{}
	Params            func(key string, defaultValue ...string) string
	Query             func(key string, defaultValue ...string) string
	Cookies           func(key string, defaultValue ...string) string
}

type Fiber interface {
	IsAlive() bool
	GetUUID() uuid.UUID
	SetUUID(uuid uuid.UUID)
	SetAttribute(key string, attribute interface{})
	GetAttribute(key string) interface{}
	GetIntAttribute(key string) int
	GetStringAttribute(key string) string
	EmitToMany(uuids []uuid.UUID, message []byte, mType ...int)
	EmitTo(uuid uuid.UUID, message []byte, mType ...int) error
	Broadcast(message []byte, except bool, mType ...int)
	Fire(event string, data []byte)
	Emit(message []byte, mType ...int)
	Close()
	pong(ctx context.Context)
	write(messageType int, messageBytes []byte)
	run()
	read(ctx context.Context)
	disconnected(err error)
	createUUID() uuid.UUID
	fireEvent(event string, data []byte, error error)
}

// EventPayload holds all information about an event
type EventPayload struct {
	Fiber            *FiberSocket
	Name             string
	SocketUUID       uuid.UUID
	SocketAttributes map[string]interface{}
	Error            error
	Data             []byte
}

type eventCallback func(payload *EventPayload)

// Source @url:https://github.com/gorilla/websocket/blob/master/conn.go#L61
// The message types are defined in RFC 6455, section 11.8.
const (
	// TextMessage denotes a text data message. The text message payload is
	// interpreted as UTF-8 encoded text data.
	TextMessage = 1
	// BinaryMessage denotes a binary data message.
	BinaryMessage = 2
	// CloseMessage denotes a close control message. The optional message
	// payload contains a numeric code and text. Use the FormatCloseMessage
	// function to format a close message payload.
	CloseMessage = 8
	// PingMessage denotes a ping control message. The optional message payload
	// is UTF-8 encoded text.
	PingMessage = 9
	// PongMessage denotes a pong control message. The optional message payload
	// is UTF-8 encoded text.
	PongMessage = 10
)

// Supported event list
const (
	// EventMessage Fired when a Text/Binary message is received
	EventMessage = "message"
	// EventPing More details here:
	// @url https://developer.mozilla.org/en-US/docs/Web/API/WebSockets_API/Writing_WebSocket_servers#Pings_and_Pongs_The_Heartbeat_of_WebSockets
	EventPing = "ping"
	EventPong = "pong"
	// EventDisconnect Fired on disconnection
	// The error provided in disconnection event
	// is defined in RFC 6455, section 11.7.
	// @url https://github.com/gofiber/websocket/blob/cd4720c435de415b864d975a9ca23a47eaf081ef/websocket.go#L192
	EventDisconnect = "disconnect"
	// EventConnect Fired on first connection
	EventConnect = "connect"
	// EventClose Fired when the connection is actively closed from the server
	EventClose = "close"
	// EventError Fired when some error appears useful also for debugging websockets
	EventError = "error"
)

// support event id
const (
	EventMessageId = "1"
	EventPingId    = "2"
	EventPongId    = "3"
	// EventDisconnect Fired on disconnection
	// The error provided in disconnection event
	// is defined in RFC 6455, section 11.7.
	// @url https://github.com/gofiber/websocket/blob/cd4720c435de415b864d975a9ca23a47eaf081ef/websocket.go#L192
	EventDisconnectId = "4"
	// EventConnect Fired on first connection
	EventConnectId = "5"
	// EventClose Fired when the connection is actively closed from the server
	EventCloseId = "6"
	// EventError Fired when some error appears useful also for debugging websockets
	EventErrorId = "7"
)

/****************************************
	Variables
****************************************/

var (
	// ErrorInvalidConnection The addressed ws connection is not available anymore
	// error data is the uuid of that connection
	ErrorInvalidConnection = errors.New("message cannot be delivered invalid/gone connection")
	// ErrorUUIDDuplication The UUID already exists in the pool
	ErrorUUIDDuplication = errors.New("UUID already exists in the available connections pool")
)

// Holds a map of all event callbacks that will be executed in the future safely
var listeners = SafeListeners{
	list: make(map[string][]eventCallback),
}

// Pool with the active connections
var pool = safePool{
	conn: make(map[uuid.UUID]Fiber),
}

var (
	PongTimeout = 1 * time.Second
	// RetrySendTimeout retry after 20 ms if there is an error
	RetrySendTimeout = 20 * time.Millisecond
	//MaxSendRetry define max retries if there are socket issues
	MaxSendRetry = 5
	// ReadTimeout Instead of reading in a for loop, try to avoid full CPU load taking some pause
	ReadTimeout = 10 * time.Millisecond
)

// Creates a new instance of GoFiberWebSocket
func New(callback func(socket *FiberSocket)) func(*fiber.Ctx) error {
	return websocket.New(func(c *websocket.Conn) {
		socket := &FiberSocket{
			ws:                c,
			EventQueue:        make(chan Event, 100),
			Done:              make(chan struct{}, 1),
			EventAttributes:   make(map[string]interface{}),
			IsConnectionAlive: true,

			// GoFiber Context Support
			Locals: func(key string) interface{} {
				return c.Locals(key)
			},
			Params: func(key string, defaultValue ...string) string {
				return c.Params(key, defaultValue...)
			},
			Query: func(key string, defaultValue ...string) string {
				return c.Query(key, defaultValue...)
			},
			Cookies: func(key string, defaultValue ...string) string {
				return c.Cookies(key, defaultValue...)
			},
		}

		// Generate uuid
		socket.UUID = socket.createUUID()

		// register the connection into the pool
		pool.New(socket)

		// execute the callback of the socket initialization
		callback(socket)

		socket.fireEvent(EventConnect, nil, nil)

		// Run the loop for the given connection
		socket.run()
	})
}

// Handles Internal Event Emission
func (fs *FiberSocket) fireEvent(event string, data []byte, error error) {
	callbacks := listeners.get(event)

	// run the callbacks for the specified event in the safe listeners
	for _, callback := range callbacks {
		callback(&EventPayload{
			Fiber:            fs,
			Name:             event,
			SocketUUID:       fs.UUID,
			SocketAttributes: fs.EventAttributes,
			Data:             data,
			Error:            error,
		})
	}
}

// Creates a UUID for the ws conn using google/uuid based on RFC 4122 and DCE 1.1
func (socket *FiberSocket) createUUID() uuid.UUID {
	return uuid.New()
}

func (socket *FiberSocket) SetUUID(uuid uuid.UUID) {
	socket.Mutex.RLock()
	defer socket.Mutex.RUnlock()
	socket.UUID = uuid
}

// Get the UUID of the *FiberSocket safely
func (socket *FiberSocket) GetUUID() uuid.UUID {
	socket.Mutex.Lock()
	defer socket.Mutex.Unlock()

	return socket.UUID
}

// Send out message queue
func (fs *FiberSocket) send(ctx context.Context) {
	for {
		select {
		case message := <-fs.EventQueue:
			if !fs.hasConn() {
				if message.retries <= MaxSendRetry {
					// retry without blocking the sending thread
					go func() {
						time.Sleep(RetrySendTimeout)
						message.retries = message.retries + 1
						fs.EventQueue <- message
					}()
				}
				continue
			}

			fs.Mutex.RLock()
			err := fs.ws.WriteMessage(message.mType, message.data)
			fs.Mutex.RUnlock()

			if err != nil {
				fs.disconnected(err)
			}
		case <-ctx.Done():
			return
		}
	}
}

// SetAttribute Set a specific attribute for the specific socket connection
func (fs *FiberSocket) SetAttribute(key string, attribute interface{}) {
	fs.Mutex.Lock()
	defer fs.Mutex.Unlock()
	fs.EventAttributes[key] = attribute
}

// GetAttribute Get a specific attribute from the socket attributes
func (fs *FiberSocket) GetAttribute(key string) interface{} {
	fs.Mutex.RLock()
	defer fs.Mutex.RUnlock()
	value, ok := fs.EventAttributes[key]
	if ok {
		return value
	}
	return nil
}

// GetIntAttribute Convenience method to retrieve an attribute as an int.
// Will panic if attribute is not an int.
func (fs *FiberSocket) GetIntAttribute(key string) int {
	fs.Mutex.RLock()
	defer fs.Mutex.RUnlock()
	value, ok := fs.EventAttributes[key]
	if ok {
		return value.(int)
	}
	return 0
}

// GetStringAttribute Convenience method to retrieve an attribute as a string.
// Will panic if attribute is not an int.
func (fs *FiberSocket) GetStringAttribute(key string) string {
	fs.Mutex.RLock()
	defer fs.Mutex.RUnlock()
	value, ok := fs.EventAttributes[key]
	if ok {
		return value.(string)
	}
	return ""
}

// Start Pong/Read/Write functions
// Needs to be blocking, otherwise the connection would close.
func (fs *FiberSocket) run() {
	ctx, cancelFunc := context.WithCancel(context.Background())

	go fs.pong(ctx)
	go fs.read(ctx)
	go fs.send(ctx)

	<-fs.Done // block until one event is sent to the done channel

	cancelFunc()
}

// pong writes a control message to the client
func (fs *FiberSocket) pong(ctx context.Context) {
	timeoutTicker := time.Tick(PongTimeout)

	for {
		select {
		case <-timeoutTicker:
			fs.write(PongMessage, []byte{})
		case <-ctx.Done():
			return
		}
	}
}

func (fs *FiberSocket) hasConn() bool {
	fs.Mutex.RLock()
	defer fs.Mutex.RUnlock()
	return fs.ws.Conn != nil
}

// Add in message queue
func (fs *FiberSocket) write(messageType int, messageBytes []byte) {
	fs.EventQueue <- Event{
		mType:   messageType,
		data:    messageBytes,
		retries: 0,
	}
}

func (fs *FiberSocket) IsAlive() bool {
	fs.Mutex.RLock()
	defer fs.Mutex.RUnlock()
	return fs.IsConnectionAlive
}

func (fs *FiberSocket) setAlive(alive bool) {
	fs.Mutex.Lock()
	defer fs.Mutex.Unlock()
	fs.IsConnectionAlive = alive
}

// When the connection closes, disconnected method
func (fs *FiberSocket) disconnected(err error) {
	fs.fireEvent(EventDisconnect, nil, err)

	// may be called multiple times from different go routines
	if fs.IsAlive() {
		close(fs.Done)
	}
	fs.setAlive(false)

	// Fire error event if the connection is
	// disconnected by an error
	if err != nil {
		fs.fireEvent(EventError, nil, err)
	}

	// Remove the socket from the pool
	pool.Delete(fs.UUID)
}

// Listen for incoming messages
// and filter by message type
func (fs *FiberSocket) read(ctx context.Context) {
	timeoutTicker := time.Tick(ReadTimeout)
	for {
		select {
		case <-timeoutTicker:
			if !fs.hasConn() {
				continue
			}

			fs.Mutex.RLock()
			mtype, msg, err := fs.ws.ReadMessage()
			fs.Mutex.RUnlock()

			if mtype == PingMessage {
				fs.fireEvent(EventPing, nil, nil)
				continue
			}

			if mtype == PongMessage {
				fs.fireEvent(EventPong, nil, nil)
				continue
			}

			if mtype == CloseMessage {
				fs.disconnected(nil)
				return
			}

			if err != nil {
				fs.disconnected(err)
				return
			}

			// We have a message and we fire the message event
			fs.fireEvent(EventMessage, msg, nil)
		case <-ctx.Done():
			return
		}
	}
}

// Handles event event emission to a specific connection socket
func (fs *FiberSocket) EmitTo(uuid uuid.UUID, message []byte, mType ...int) error {

	if !pool.DoesContain(uuid) || !pool.Get(uuid).IsAlive() {

		fs.fireEvent(EventError, uuid[:], ErrorInvalidConnection)
		return ErrorInvalidConnection
	}

	pool.Get(uuid).Emit(message, mType...)
	return nil
}

// EmitToList Emit the message to a specific socket uuids list
func (fs *FiberSocket) EmitToMany(uuids []uuid.UUID, message []byte, mType ...int) {
	for _, uuid := range uuids {
		err := fs.EmitTo(uuid, message, mType...)
		if err != nil {
			fs.fireEvent(EventError, message, err)
		}
	}
}

// Close Actively close the connection from the server
func (fs *FiberSocket) Close() {
	fs.write(CloseMessage, []byte("Connection closed"))
	fs.fireEvent(EventClose, nil, nil)
}

// Emits a message to the client connection
func (fs *FiberSocket) Emit(message []byte, mType ...int) {
	t := TextMessage

	if len(mType) > 0 {
		t = mType[0]
	}
	fs.write(t, message)
}

// Adds listener callback to an event into the safe listeners list
func On(event string, callback eventCallback) {
	listeners.set(event, callback)
}

// Broadcast message to all the active connections
// except avoid broadcasting the message to itself
func (fs *FiberSocket) Broadcast(message []byte, except bool, mType ...int) {
	for uuid := range pool.GetAll() {
		if except && fs.UUID == uuid {
			continue
		}
		err := fs.EmitTo(uuid, message, mType...)
		if err != nil {
			fs.fireEvent(EventError, message, err)
		}
	}
}

// Fire custom event
func (fs *FiberSocket) Fire(event string, data []byte) {
	fs.fireEvent(event, data, nil)
}

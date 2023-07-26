package socketio

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/googollee/go-socket.io/engineio"
	"github.com/googollee/go-socket.io/engineio/transport"
	"github.com/googollee/go-socket.io/engineio/transport/polling"
	"github.com/googollee/go-socket.io/logger"
	"github.com/googollee/go-socket.io/parser"
)

// Server is a go-socket.io server.
type Client struct {
	engine *conn

	handlers *namespaceHandlers
}

// NewServer returns a server.
func NewClient(uri string, opts *engineio.Options) (*Client, error) {
	enginioCon, err := Dial(uri, opts)
	if err != nil {
		return nil, err
	}

	newconn := newConn(enginioCon, newNamespaceHandlers())

	client := &Client{
		engine:   newconn,
		handlers: newconn.handlers,
	}

	client.OnEvent("/", "asd", func(s Conn, msg interface{}) interface{} {
		logger.Info("Receive Message /dbAdd: ", "type", fmt.Sprintf("%T", msg), "msg", msg)
		return nil
	})

	client.OnConnect("/", func(s Conn) error {
		// Called on every successful Get Request
		fmt.Println("OnConnect HANDLER")
		return nil
	})

	client.serveConn(newconn)

	time.Sleep(1 * time.Second)

	fmt.Println("EMIT")
	ns := newNamespaceConn(newconn, "/", nil)
	ns.Emit("message", struct{}{},
		func(reply interface{}) error {
			fmt.Println("REEPPLLLYY", reply)
			return nil
		},
	)

	fmt.Println("DONE EMIT")
	return client, nil
}

func Dial(uri string, opts *engineio.Options) (engineio.Conn, error) {
	// Process URL
	url, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	url.Path = path.Join("/socket.io", url.Path)
	url.Path = url.EscapedPath()
	if strings.HasSuffix(url.Path, "socket.io") {
		url.Path += "/"
	}

	// Create dialler
	dialer := engineio.Dialer{
		Transports: []transport.Transport{polling.Default},
	}
	conn, err := dialer.Dial(url.String(), nil)
	if err != nil {
		return nil, err
	}
	fmt.Println(conn.ID(), conn.LocalAddr(), "->", conn.RemoteAddr(), "with", conn.RemoteHeader())
	return conn, nil
}

// Close closes server.
func (s *Client) Close() error {
	return s.engine.Close()
}

// OnConnect set a handler function f to handle open event for namespace.
func (s *Client) OnConnect(namespace string, f func(Conn) error) {
	h := s.getNamespace(namespace)
	if h == nil {
		h = s.createNamespace(namespace)
	}

	h.OnConnect(f)
}

// OnDisconnect set a handler function f to handle disconnect event for namespace.
func (s *Client) OnDisconnect(namespace string, f func(Conn, string)) {
	h := s.getNamespace(namespace)
	if h == nil {
		h = s.createNamespace(namespace)
	}

	h.OnDisconnect(f)
}

// OnError set a handler function f to handle error for namespace.
func (s *Client) OnError(namespace string, f func(Conn, error)) {
	h := s.getNamespace(namespace)
	if h == nil {
		h = s.createNamespace(namespace)
	}

	h.OnError(f)
}

// OnEvent set a handler function f to handle event for namespace.
func (s *Client) OnEvent(namespace, event string, f interface{}) {
	h := s.getNamespace(namespace)
	if h == nil {
		h = s.createNamespace(namespace)
	}

	h.OnEvent(event, f)
}

func (s *Client) serveConn(c *conn) {
	if err := c.connectClient(); err != nil {
		_ = c.Close()
		if root, ok := s.handlers.Get(rootNamespace); ok && root.onError != nil {
			root.onError(nil, err)
		}

		return
	}

	go s.clientError(c)
	go s.clientWrite(c)
	go s.clientRead(c)
}

func (s *Client) clientError(c *conn) {
	defer func() {
		if err := c.Close(); err != nil {
			logger.Error("close connect:", err)
		}

	}()

	for {
		select {
		case <-c.quitChan:
			return
		case err := <-c.errorChan:
			fmt.Println("Server Error", err)
			var errMsg *errorMessage
			if !errors.As(err, &errMsg) {
				continue
			}

			if handler := c.namespace(errMsg.namespace); handler != nil {
				if handler.onError != nil {
					nsConn, ok := c.namespaces.Get(errMsg.namespace)
					if !ok {
						continue
					}
					handler.onError(nsConn, errMsg.err)
				}
			}
		}
	}
}

func (s *Client) clientWrite(c *conn) {
	defer func() {
		if err := c.Close(); err != nil {
			logger.Error("close connect:", err)
		}

	}()

	fmt.Println("Client Write")
	for {
		fmt.Println("Waiting to read")
		select {
		case <-c.quitChan:
			fmt.Println("quitChan")
			return
		case pkg := <-c.writeChan:
			fmt.Println("Client ssssss", len(c.writeChan), pkg.Header, pkg.Data)
			if err := c.encoder.Encode(pkg.Header, pkg.Data); err != nil {
				c.onError(pkg.Header.Namespace, err)
				fmt.Println("Client errrr", err)
			}
		}
	}
}

func (s *Client) clientRead(c *conn) {
	defer func() {
		if err := c.Close(); err != nil {
			logger.Error("close connect:", err)
		}
		fmt.Println("Client Read Stopped")
	}()

	fmt.Println("Client Read Here and ready")
	var event string

	for {
		fmt.Println("Client Read loop")
		var header parser.Header

		if err := c.decoder.DecodeHeader(&header, &event); err != nil {
			c.onError(rootNamespace, err)
			fmt.Println("aaaa", rootNamespace, err)
			return
		}

		if header.Namespace == aliasRootNamespace {
			header.Namespace = rootNamespace
		}

		var err error
		switch header.Type {
		case parser.Ack:
			fmt.Println("Acky", event)
			err = clientAckPacketHandler(c, header)
		case parser.Connect:
			fmt.Println("CONNECT PACKET RECEIVED", header)

			err = clientConnectPacketHandler(c, header)
		case parser.Disconnect:
			fmt.Println("Disconnect")
			err = clientDisconnectPacketHandler(c, header)
		case parser.Event:
			fmt.Println("Event")
			err = clientEventPacketHandler(c, event, header)
		}

		if err != nil {
			logger.Error("client read:", err)
			fmt.Println("dddd")
			return
		}
	}
}

func (s *Client) createNamespace(nsp string) *namespaceHandler {
	if nsp == aliasRootNamespace {
		nsp = rootNamespace
	}

	handler := newNamespaceHandler(nsp, nil)
	s.handlers.Set(nsp, handler)

	return handler
}

func (s *Client) getNamespace(nsp string) *namespaceHandler {
	if nsp == aliasRootNamespace {
		nsp = rootNamespace
	}

	ret, ok := s.handlers.Get(nsp)
	if !ok {
		return nil
	}

	return ret
}

////
// Handlers
////

func (c *conn) connectClient() error {
	fmt.Println("CONNECTION ONCE")
	rootHandler, ok := c.handlers.Get(rootNamespace)
	if !ok {
		return errUnavailableRootHandler
	}

	root := newNamespaceConn(c, aliasRootNamespace, rootHandler.broadcast)
	c.namespaces.Set(rootNamespace, root)

	root.Join(root.Conn.ID())

	c.namespaces.Range(func(ns string, nc *namespaceConn) {
		nc.SetContext(c.Conn.Context())
	})

	header := parser.Header{
		Type: parser.Connect,
	}

	if err := c.encoder.Encode(header); err != nil {
		return err
	}

	return nil
}

func clientAckPacketHandler(c *conn, header parser.Header) error {
	conn, ok := c.namespaces.Get(header.Namespace)
	if !ok {
		_ = c.decoder.DiscardLast()
		return nil
	}

	var x interface{}
	x = "asd"
	args, err := c.decoder.DecodeArgs([]reflect.Type{reflect.TypeOf(x)})

	fmt.Println("THISISCRAZY", args, err)
	conn.dispatch(header)

	return nil
}

func clientEventPacketHandler(c *conn, event string, header parser.Header) error {
	conn, ok := c.namespaces.Get(header.Namespace)
	if !ok {
		_ = c.decoder.DiscardLast()
		return nil
	}

	handler, ok := c.handlers.Get(header.Namespace)
	if !ok {
		_ = c.decoder.DiscardLast()
		logger.Info("missing handler for namespace", "namespace", header.Namespace)
		return nil
	}

	args, err := c.decoder.DecodeArgs(handler.getEventTypes(event))
	if err != nil {
		logger.Info("Error decoding the message type", "namespace", header.Namespace, "event", event, "eventType", handler.getEventTypes(event), "err", err.Error())
		c.onError(header.Namespace, err)
		return errDecodeArgs
	}

	ret, err := handler.dispatchEvent(conn, event, args...)
	if err != nil {
		logger.Info("Error for event type", "namespace", header.Namespace, "event", event)
		c.onError(header.Namespace, err)
		return errHandleDispatch
	}

	if len(ret) > 0 {
		header.Type = parser.Ack
		c.write(header, ret...)
	} else {
		logger.Info("missing event handler for namespace", "namespace", header.Namespace, "event", event)
	}

	return nil
}

func clientConnectPacketHandler(c *conn, header parser.Header) error {
	if err := c.decoder.DiscardLast(); err != nil {
		logger.Info("connectPacketHandler DiscardLast", err, "namespace", header.Namespace)
		c.onError(header.Namespace, err)
		return nil
	}

	handler, ok := c.handlers.Get(header.Namespace)
	if !ok {
		logger.Info("connectPacketHandler get namespace handler", "namespace", header.Namespace)
		c.onError(header.Namespace, errFailedConnectNamespace)
		return errFailedConnectNamespace
	}

	conn, ok := c.namespaces.Get(header.Namespace)
	if !ok {
		conn = newNamespaceConn(c, header.Namespace, handler.broadcast)
		c.namespaces.Set(header.Namespace, conn)
		conn.Join(c.Conn.ID())
	}

	_, err := handler.dispatch(conn, header)
	if err != nil {
		logger.Info("connectPacketHandler  dispatch", "namespace", header.Namespace)
		log.Println("dispatch connect packet", err)
		c.onError(header.Namespace, err)
		return errHandleDispatch
	}

	return nil
}

func clientDisconnectPacketHandler(c *conn, header parser.Header) error {
	args, err := c.decoder.DecodeArgs(defaultHeaderType)
	if err != nil {
		c.onError(header.Namespace, err)
		return errDecodeArgs
	}

	conn, ok := c.namespaces.Get(header.Namespace)
	if !ok {
		_ = c.decoder.DiscardLast()
		return nil
	}

	conn.LeaveAll()

	c.namespaces.Delete(header.Namespace)

	handler, ok := c.handlers.Get(header.Namespace)
	if !ok {
		return nil
	}

	_, err = handler.dispatch(conn, header, args...)
	if err != nil {
		log.Println("dispatch disconnect packet", err)
		c.onError(header.Namespace, err)
		return errHandleDispatch
	}

	return nil
}

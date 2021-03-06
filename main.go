package main

import (
	"log"
	"net/http"
	"sync"

	"github.com/gomodule/redigo/redis"
	"github.com/gorilla/websocket"
	"github.com/satori/go.uuid"
)

type User struct {
	ID   string
	conn *websocket.Conn
}

type Store struct {
	Users []*User
	sync.Mutex
}

type Message struct {
	DeliveryID string `json:"id"`
	Content    string `json:"content"`
}

var (
	gStore      *Store
	gPubSubConn *redis.PubSubConn
	gRedisConn  = func() (redis.Conn, error) {
		return redis.Dial("tcp", ":6379")
	}

	serverAddress = ":8080"
	channelName   = "myChannel"
)

func init() {
	gStore = &Store{
		Users: make([]*User, 0, 1),
	}
}

func main() {
	gRedisConn, err := gRedisConn()
	if err != nil {
		panic(err)
	}

	defer gRedisConn.Close()

	gPubSubConn = &redis.PubSubConn{Conn: gRedisConn}
	if err := gPubSubConn.Subscribe(channelName); err != nil {
		panic(err)
	}

	go deliverMessages()

	http.HandleFunc("/ws", wsHandler)
	log.Printf("server started at %s\n", serverAddress)
	log.Fatal(http.ListenAndServe(serverAddress, nil))
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrader error %s\n" + err.Error())
		return
	}

	u := gStore.newUser(conn)
	log.Printf("user %s has joined\n", u.ID)

	for {
		var m Message
		if err := u.conn.ReadJSON(&m); err != nil {
			log.Printf("error on ws. message %s\n", err)
			continue
		}

		if c, err := gRedisConn(); err != nil {
			log.Printf("error on redis conn. %s\n", err)
		} else {
			log.Printf("going to publish message %s\n", string(m.Content))
			c.Do("PUBLISH", channelName, string(m.Content))
		}
	}
}

func (s *Store) newUser(conn *websocket.Conn) *User {
	u := &User{
		ID:   uuid.Must(uuid.NewV4()).String(),
		conn: conn,
	}

	s.Lock()
	defer s.Unlock()

	s.Users = append(s.Users, u)
	return u
}

func deliverMessages() {
	for {
		switch v := gPubSubConn.Receive().(type) {
		case redis.Message:
			// gStore.findAndDeliver(v.Channel, string(v.Data))
			gStore.broadcast(string(v.Data))
		case redis.Subscription:
			log.Printf("subscription message: %s: %s %d\n", v.Channel, v.Kind, v.Count)
		case error:
			log.Println("error pub/sub, delivery has stopped")
			return
		default:
			log.Printf("pubsub received")
		}
	}
}

func (s *Store) broadcast(content string) {
	m := Message{
		Content: content,
	}

	for _, u := range s.Users {
		if err := u.conn.WriteJSON(m); err != nil {
			log.Printf("error on message delivery e: %s\n", err)
		} else {
			log.Printf("message sent to user %s\n", u.ID)
		}
	}
}

func (s *Store) findAndDeliver(userID string, content string) {
	m := Message{
		Content: content,
	}

	for _, u := range s.Users {
		if u.ID == userID {
			if err := u.conn.WriteJSON(m); err != nil {
				log.Printf("error on message delivery e: %s\n", err)
			} else {
				log.Printf("user %s found, message sent\n", userID)
			}

			return
		}
	}

	log.Printf("user %s not found at out store\n", userID)
}

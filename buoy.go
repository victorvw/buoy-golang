package main

import (
    "encoding/json"
    "flag"
    "io/ioutil"
    "log"
    "net/http"
    "time"
    "github.com/google/uuid"
    "github.com/gorilla/mux"
    "github.com/gorilla/websocket"
    // "github.com/davecgh/go-spew/spew"
)

var addr = flag.String("addr", "localhost:8080", "http service address")

type JsonFormat struct {
    A string `json:"a"`
    Bn int `json:"bn"`
    Sig string `json:"sig"`
    T *json.RawMessage `json:"t"`
    Tx string `json:"tx"`
}

type Connection struct {
    ws *websocket.Conn
    send chan []byte
}

const (
    writeWait = 10 * time.Second
    pongWait = 60 * time.Second
    pingPeriod = (pongWait * 9) / 10
    maxMessageSize = 512
)

var upgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,
    CheckOrigin: func(r *http.Request) bool {
        return true
    },
}

func (s Subscription) readPump() {
    c := s.conn
    defer func() {
        h.unregister <- s
        c.ws.Close()
    }()
    c.ws.SetReadLimit(maxMessageSize)
    c.ws.SetReadDeadline(time.Now().Add(pongWait))
    c.ws.SetPongHandler(func(string) error { c.ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
    for {
        _, msg, err := c.ws.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
                log.Printf("error: %v", err)
            }
            break
        }
        m := Message{msg, s.uuid}
        h.broadcast <- m
    }
}

func (c *Connection) write(mt int, payload []byte) error {
    c.ws.SetWriteDeadline(time.Now().Add(writeWait))
    return c.ws.WriteMessage(mt, payload)
}

func (s *Subscription) writePump() {
    c := s.conn
    ticker := time.NewTicker(pingPeriod)
    defer func() {
        ticker.Stop()
        c.ws.Close()
    }()
    for {
        select {
        case message, ok := <-c.send:
            if !ok {
                c.write(websocket.CloseMessage, []byte{})
                return
            }
            if err := c.write(websocket.TextMessage, message); err != nil {
                return
            }
        case <-ticker.C:
            if err := c.write(websocket.PingMessage, []byte{}); err != nil {
                return
            }
        }
    }
}

func validUUID(u string) bool {
    _, err := uuid.Parse(u)
    return err == nil
}

func readPost(w http.ResponseWriter, r *http.Request) (body []byte) {
    body, err := ioutil.ReadAll(r.Body)
    defer r.Body.Close()
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    return body
}

func readJson(w http.ResponseWriter, body []byte) (data JsonFormat) {
    err := json.Unmarshal(body, &data)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    return data
}

func handlePost(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    // Ensure validity of UUID
    if validUUID(vars["uuid"]) {
        // Read the POST body
        body := readPost(w, r)
        // Convert it to the `JsonFormat` struct
        data := readJson(w, body)
        // Convert back to JSON to cleanse
        jsonString, jsonErr := json.Marshal(data)
        if jsonErr == nil {
            // Find connections matching the UUID
            connections := h.uuids[vars["uuid"]]
            if connections != nil {
                // Send the message along to any matching channels
                for c := range connections {
                    c.send <- []byte(jsonString)
                }
            } else {
                // No matching UUID channels
            }
        }
    } else {
        // Invalid UUID submitted to
    }
}

func handleConnection(w http.ResponseWriter, r *http.Request) {
    ws, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Print("upgrade:", err)
        return
    }
    vars := mux.Vars(r)
    // Ensure validity of UUID
    if validUUID(vars["uuid"]) {
        // Establish connection and subscription
        c := &Connection{send: make(chan []byte, 256), ws: ws}
        s := Subscription{c, vars["uuid"]}
        h.register <- s
        go s.writePump()
        s.readPump()
    } else {
        // Invalid UUID subscribed to
        ws.Close()
    }
}

func main() {
    flag.Parse()
    log.SetFlags(0)

    go h.run()

    rtr := mux.NewRouter()
    // Handle POST connections
    rtr.HandleFunc("/{uuid}", handlePost).Methods("POST")
    // Handle websocket connections
    rtr.HandleFunc("/{uuid}", handleConnection)

    http.Handle("/", rtr)

    log.Println("Listening...")
    log.Fatal(http.ListenAndServe(*addr, nil))
}

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/avct/uasurfer"
	"github.com/gomodule/redigo/redis"
)

var pool *redis.Pool

var SVG = "<?xml version=\"1.0\" encoding=\"UTF-8\" ?><svg xmlns=\"http://www.w3.org/2000/svg\" version=\"1.1\" width=\"1\" height=\"1\"></svg>"

func main() {

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	pool = &redis.Pool{
		MaxIdle:     10,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			return redis.Dial("tcp", "localhost:6379")
		},
	}

	mux := http.NewServeMux()
	fs := http.FileServer(http.Dir("./static"))
	mux.Handle("/", fs)
	mux.HandleFunc("/track", Track)
	mux.HandleFunc("/register", Register)
	mux.HandleFunc("/dashboard", Dashboard)

	fmt.Println("Listening...")
	http.ListenAndServe("127.0.0.1:8000", mux)
}

func hash(stri string) string {
	h := sha256.Sum256([]byte(stri))
	return string(h[:])

}

func Track(w http.ResponseWriter, r *http.Request) {
	conn := pool.Get()
	defer conn.Close()


	user := r.PostFormValue("site")
	if user == "" {
		http.Error(w, "missing uid", http.StatusBadRequest)
		return
	}

	ref := r.PostFormValue("referrer")
	if ref != "" {
		conn.Send("ZINCRBY", "ref:"+user, 1, ref)
	}
	utcoffset, err := strconv.Atoi(r.PostFormValue("utcoffset"))
	if err != nil {
		utcoffset = 0
	}

	loc := r.Header.Get("Location")
	if loc != "" {
		conn.Send("ZINCRBY", "loc:"+user, 1, loc)
	}

	location, err := time.LoadLocation("UTC")
	if err != nil {
		log.Println(user, err)
		http.Error(w, err.Error(), 500)
		return
	}
	utcnow := time.Now().In(location)
	now := utcnow.Add(time.Hour * time.Duration(utcoffset))
	conn.Send("HINCRBY", "date:"+user, now.Format("2006-01-02"), 1)
	conn.Send("HINCRBY", "weekday:"+user, int(now.Weekday()), 1)
	conn.Send("HINCRBY", "hour:"+user, now.Hour(), 1)

	userAgent := r.Header.Get("User-Agent")
	ua := uasurfer.Parse(userAgent)
	conn.Send("HINCRBY", "browser:"+user, ua.Browser.Name.StringTrimPrefix(), 1)
	conn.Send("HINCRBY", "device:"+user, ua.DeviceType.StringTrimPrefix(), 1)
	conn.Send("HINCRBY", "platform:"+user, ua.OS.Platform.StringTrimPrefix(), 1)

	w.Header().Set("Content-Type", "image/svg+xml")
	fmt.Fprint(w, SVG)
}

func Register(w http.ResponseWriter, r *http.Request) {
	conn := pool.Get()
	defer conn.Close()

	user := r.FormValue("user")
	password := r.FormValue("password")
	if user == "" || password == "" {
		http.Error(w, "Missing Input", http.StatusBadRequest)
		return
	}

	if len(user) < 4 {
		http.Error(w, "User must have at least 4 charachters", http.StatusBadRequest)
		return
	}

	if len(password) < 8 {
		http.Error(w, "Password must have at least 4 charachters", http.StatusBadRequest)
		return
	}

	// also delete anything saved by this user before!!
	res, err := redis.Int64(conn.Do("HSETNX", "users", user, hash(password)))
	if err != nil {
		log.Println(user, err)
		http.Error(w, err.Error(), 500)
		return
	}
	if res == 0 {
		http.Error(w, "Username taken", http.StatusBadRequest)
	} else {
		userData, err := getData(user, conn)
		if err != nil {
			log.Println(user, err)
			http.Error(w, err.Error(), 500)
			return
		}
		jsonString, err := json.Marshal(userData)
		if err != nil {
			log.Println(user, err)
			http.Error(w, err.Error(), 500)
			return
		}
		fmt.Fprintln(w, string(jsonString))
	}
}

func Dashboard(w http.ResponseWriter, r *http.Request) {
	conn := pool.Get()
	defer conn.Close()

	user := r.FormValue("user")
	password := r.FormValue("password")
	if user == "" || password == "" {
		http.Error(w, "Missing Input", http.StatusBadRequest)
		return
	}

	res, err := redis.String(conn.Do("HGET", "users", user))
	if err != nil {
		log.Println(user, err)
		http.Error(w, err.Error(), 500)
		return
	}
	if res == hash(password) {
		userData, err := getData(user, conn)
		if err != nil {
			log.Println(user, err)
			http.Error(w, err.Error(), 500)
			return
		}
		jsonString, err := json.Marshal(userData)
		if err != nil {
			log.Println(user, err)
			http.Error(w, err.Error(), 500)
			return
		}
		fmt.Fprintln(w, string(jsonString))
	} else {
		http.Error(w, "Wrong username or password", http.StatusBadRequest)
	}
}

func getData(user string, conn redis.Conn) (map[string]map[string]int, error) {

	m := make(map[string]map[string]int)

	resp, err := redis.IntMap(conn.Do("HGETALL", "date:4"))
	if err != nil {
		log.Println(user, err)
		return nil, err
	}
	m["date"] = resp

	m["loc"], err = redis.IntMap(conn.Do("ZRANGE", "loc:"+user, 0, -1, "WITHSCORES"))
	if err != nil {
		log.Println(user, err)
		return nil, err
	}

	m["ref"], err = redis.IntMap(conn.Do("ZRANGE", "ref:"+user, 0, -1, "WITHSCORES"))
	if err != nil {
		log.Println(user, err)
		return nil, err
	}

	m["weekday"], err = redis.IntMap(conn.Do("HGETALL", "weekday:"+user))
	if err != nil {
		log.Println(user, err)
		return nil, err
	}

	m["hour"], err = redis.IntMap(conn.Do("HGETALL", "hour:"+user))
	if err != nil {
		log.Println(user, err)
		return nil, err
	}

	m["browser"], err = redis.IntMap(conn.Do("HGETALL", "browser:"+user))
	if err != nil {
		log.Println(user, err)
		return nil, err
	}

	m["device"], err = redis.IntMap(conn.Do("HGETALL", "device:"+user))
	if err != nil {
		log.Println(user, err)
		return nil, err
	}

	m["platform"], err = redis.IntMap(conn.Do("HGETALL", "platform:"+user))
	if err != nil {
		log.Println(user, err)
		return nil, err
	}

	return m, nil
}
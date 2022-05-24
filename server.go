package main

import (
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/json-iterator/go"
	"log"
	"os"
	"io"
	"math/rand"
	"net/http"
	"nhooyr.io/websocket"
	"strconv"
	"sync"
	"time"
	"runtime/debug"
	"context"
	"io/ioutil"
)
var BatteryStatusIndex = [3]string{"full","charging","discharging"}
var NetworkTypeIndex = [3]string{"wifi","mobile","none"}
func room_cleanup(roomid int){
    go func() {
	tchan := time.After(time.Second * 10)
	<-tchan
	if existroom(roomid)&&isroomempty(roomid) {
	    roomdic.Delete(roomid)
		log.Println("room deleted(长时间无人进入): ", roomid)}
	}()
}
func send(roomid int, deviceid int,data []byte){
    log.Println(string(data),"->",deviceid,roomid)
    defer func(){
        err := recover()
		if err != nil {
		    log.Println("ws send failed",string(data),roomid,deviceid)
		    debug.PrintStack()
		}
	}()
	t,_ := room(roomid).Deviceindex.Load(deviceid)
	device := t.(*device_struct)
    device.wsconn.Write(device.cxt, websocket.MessageText, data)
}
func send_broadcast(roomid int,data []byte){
	for _, device := range room(roomid).Devicelist {
	   go send(roomid,device.Id,data)
	}
}
func GetJSON(obj interface{}, ignoreFields ...string) (map[string]interface{}, error) {
	toJson, err := json.Marshal(obj)
	if err != nil {
		return map[string]interface{}{}, err
	}

	toMap := map[string]interface{}{}
	json.Unmarshal([]byte(string(toJson)), &toMap)

	for _, field := range ignoreFields {
		delete(toMap, field)
	}
	return toMap, nil
}

func KEY() string {
	var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")
	b := make([]rune, 32)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func timestamp() int {
	var timestamp = time.Now().UnixNano()/ 1e6
	return int(timestamp)
}

/////////////////////////////////////////////////
type verify_roomtoken_struct struct {
	Success     bool   `json:"success"`
	Description string `json:"description"`
}

func verify_roomtoken(roomid int, roomtoken string) verify_roomtoken_struct {
	var t verify_roomtoken_struct
	if roomid == 0 || roomtoken == "" {
		t.Success = false
		t.Description = "missing parameters"
		return t
	}
	temp, ok := roomdic.Load(roomid)
	if !ok {
		t.Success = false
		t.Description = "room not found"
		return t
	}
	room := temp.(*room_struct)
	if room.Token != roomtoken {
		t.Success = false
		t.Description = "roomtoken not match"
		return t
	}
	t.Success = true
	return t
}

////////////////////////////////////////////////
func ws(w http.ResponseWriter, r *http.Request) {
	roomid, _ := strconv.Atoi(mux.Vars(r)["roomid"])
	roomtoken := mux.Vars(r)["roomtoken"]
	version   := mux.Vars(r)["version"]
	
	log.Println(version)

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		log.Println(err)
		return
	}
	log.Println("connection established：", r.RemoteAddr)
	defer conn.Close(websocket.StatusNormalClosure, "")

	result := verify_roomtoken(roomid, roomtoken)
	if !result.Success {
		var m2 common_string_struct
	    m2.Action = "showexitdialog"
	    m2.Data = result.Description
	    data, _ := json.Marshal(m2)
		conn.Write(r.Context(), websocket.MessageText, data)
		return
	}

	devicecount++
	var device device_struct
	device.Status = "preparing"
	device.Id = devicecount
	device.Master = true
	device.Token = KEY()
	device.wsconn = conn
	device.cxt = r.Context()
	room(roomid).Devicelist = append(room(roomid).Devicelist, &device)
	room(roomid).Deviceindex.Store(device.Id,&device)

	defer func() {
		room(roomid).Deviceindex.Delete(device.Id)
		removedevice(roomid, device.Id)
		log.Println("设备已删除：", device, roomid)
		if isroomempty(roomid) {
			room_cleanup(roomid)
			return
		}
		broadcast_setindex(roomid)
		broadcast_setdevicelist(roomid)
		broadcast_sendmsg(roomid, device.Brand+device.Model+" left")
	}()


	//go broadcast_syncall(roomid)
	go initcanvas(roomid, device.Id)
	go setindex(roomid, device.Id)
	go broadcast_setdevicelist(roomid)
	for {

		_, data, err := conn.Read(r.Context())
		if err != nil {
			log.Println("连接断开：", r.RemoteAddr)
			log.Println(err)
			return
		}
		log.Println("received：", string(data))

		//conn.Write(r.Context(),websocket.MessageText,data)

		go func() {

			defer func() {
				err := recover()
				if err != nil {
					log.Println("recovered from panic：",err)
					debug.PrintStack()
				}
			}()

			var parsed map[string]interface{}
			err = json.Unmarshal(data, &parsed)
			if err != nil {
				log.Println("parse websocket json failed", err)
				return
			}
			switch parsed["action"] {

			case "settextcolor":
				room(roomid).Textcolor = parsed["data"].(string)
				go broadcast_settextcolor(roomid)
				break
			case "setbgcolor":
				room(roomid).Bgcolor = parsed["data"].(string)
				go broadcast_setbgcolor(roomid)
				break
			case "settextsize":
				room(roomid).Textsize = parsed["data"].(float64)
				go broadcast_settextsize(roomid)
				break
			case "settext":
				room(roomid).Text = parsed["data"].(string)
				go broadcast_settext(roomid)
				break
			case "setspeed":
				room(roomid).Speed = parsed["data"].(float64)
				go broadcast_setspeed(roomid)
				break
			case "setbgblinkinterval":
				room(roomid).Bgblinkinterval = parsed["data"].(float64)
				go broadcast_setbgblinkinterval(roomid)
				break
			case "seteffect":
				room(roomid).Effect = parsed["data"].(string)
				go broadcast_seteffect(roomid)
				break
			case "setbrightness":
				brightness := parsed["data"].(int)
				target := parsed["target"].(int)
				if target == -1 {
					room(roomid).Brightness = brightness
					broadcast_setbrightness(roomid)
				} else {
					setbrightness(roomid,target,brightness)
				}
				break

			case "statusreport":
				device.Batterylevel = int(parsed["batterylevel"].(float64))
				device.Batterystatus = BatteryStatusIndex [int(parsed["batterystatus"].(float64))]
				break
			case "reportscreensize":
				device.Screenheight = parsed["screenheight"].(float64)
				device.Screenwidth = parsed["screenwidth"].(float64)
				device.Safezone = parsed["safezone"].(float64)
				go broadcast_setdevicelist(roomid)
				break
			case "getdevicelist":
				go setdevicelist(roomid, device.Id)
				break
			case "ping":
				//device.chanAddr <- []byte("pong")
				break
			case "timestamp":
				var m common_int_struct
				m.Action = "timestamp"
				m.Data = timestamp()
				data, _ := json.Marshal(m)
				send(roomid,device.Id,data)
				break
			case "init":
				device.Brand = parsed["brand"].(string)
				device.Model = parsed["model"].(string)
				device.Platform = parsed["platform"].(string)
				device.Nettype = NetworkTypeIndex[int(parsed["nettype"].(float64))]
				device.Status = "normal"
				go broadcast_setdevicelist(roomid)
				go setindex(roomid, device.Id)
				go broadcast_sendmsg(roomid, device.Brand+" "+device.Model+" joined")
				break
			}

		}()

	}

}

//////////////////////////////////////////////////////
func existroom(roomid int)bool{
	_,ok := roomdic.Load(roomid)
	return ok
}
func isroomempty(roomid int) bool {
	return len(room(roomid).Devicelist) == 0
}
func getselfindex(roomid int, deviceid int) int {
	for i := 0; i < len(room(roomid).Devicelist); i++ {
		if room(roomid).Devicelist[i].Id == deviceid {
			return i
		}
	}
	return -1
}
func ismaster(roomid int, deviceid int) bool {
	i := getselfindex(roomid, deviceid)
	return room(roomid).Devicelist[i].Master
}
func removedevice(roomid int, deviceid int) {
	i := getselfindex(roomid, deviceid)
	room(roomid).Devicelist = append(room(roomid).Devicelist[:i], room(roomid).Devicelist[i+1:]...)
}

/////////////////////////////////////////////////////
type syncall_struct struct {
	Action string       `json:"action"`
	Data   *room_struct `json:"data"`
}

func initcanvas(roomid int, deviceid int) {
	var m2 common_interface_struct
	m2.Action = "init"
	m2.Data, _ = GetJSON(room(roomid), "devicelist")
	data, _ := json.Marshal(m2)
	send(roomid, deviceid,data)
}
type devicelist_struct struct {
	Id            int     `json:"id"`
	Safezone      float64 `json:"safezone"`
	Screenwidth   float64 `json:"screenwidth"`
	Screenheight  float64 `json:"screenheight"`
}
type setdevicelist_struct struct {
	Action string           `json:"action"`
	Data   []*devicelist_struct `json:"data"`
}
func getdevicelist(roomid int)[]*devicelist_struct{
	var m2 []*devicelist_struct
	for _, t := range room(roomid).Devicelist {
		var m1 devicelist_struct
		m1.Id=t.Id
		m1.Safezone=t.Safezone
		m1.Screenwidth=t.Screenwidth
		m1.Screenheight=t.Screenheight
		m2 = append(m2, &m1)
	}
	return m2
}
func setdevicelist(roomid int, deviceid int) {
	if room(roomid) == nil {
		return
	}
	var m2 setdevicelist_struct
	m2.Action = "setdevicelist"
    m2.Data=getdevicelist(roomid)
	data, _ := json.Marshal(m2)
	send(roomid, deviceid,data)
}
func broadcast_setdevicelist(roomid int) {
	if room(roomid) == nil {
		return
	}
	var m2 setdevicelist_struct
	m2.Action = "setdevicelist"
    m2.Data=getdevicelist(roomid)
	data, _ := json.Marshal(m2)
	send_broadcast(roomid,data)
}
func broadcast_syncall(roomid int) {
	if room(roomid) == nil {
		return
	}
	var m2 syncall_struct
	m2.Action = "syncall"
	m2.Data = room(roomid)
	data, _ := json.Marshal(m2)
	send_broadcast(roomid,data)
}

type selfindexdatastruct struct {
	Action string `json:"action"`
	Index  int    `json:"index"`
	Id     int    `json:"deviceid"`
	Token  string `json:"token"`
}

func broadcast_setindex(roomid int) {
	t := room(roomid).Devicelist
	for i := 0; i < len(t); i++ {
		deviceid := t[i].Id
		var m2 selfindexdatastruct
		m2.Action = "setindex"
		m2.Index = getselfindex(roomid, deviceid) + 1
		m2.Id = deviceid
		m2.Token = t[i].Token
		data, _ := json.Marshal(m2)
		send(roomid,i,data)
	}
}
func setindex(roomid int, deviceid int) {
	i := getselfindex(roomid, deviceid)
	var m2 selfindexdatastruct
	m2.Action = "setindex"
	m2.Index = i + 1
	m2.Id = deviceid
	m2.Token = room(roomid).Devicelist[i].Token
	data, _ := json.Marshal(m2)
	send(roomid, deviceid,data)
}

type sendmsgstruct struct {
	Action string `json:"action"`
	Msg    string `json:"msg"`
}

func sendmsg(roomid int, deviceid int, msg string) {
	var m sendmsgstruct
	m.Action = "msg"
	m.Msg = msg
	data, _ := json.Marshal(m)
	send(roomid, deviceid,data)
}
func broadcast_sendmsg(roomid int, msg string) {
	var m2 sendmsgstruct
	m2.Action = "msg"
	m2.Msg = msg
	data, _ := json.Marshal(m2)
	send_broadcast(roomid,data)

}

type single_action_struct struct {
	Action string `json:"action"`
}

func forcequit(roomid int, deviceid int) {
	var m single_action_struct
	m.Action = "forcequit"
	data, _ := json.Marshal(m)
	send(roomid, deviceid,data)
}
func forceresync(roomid int, deviceid int) {
	var m single_action_struct
	m.Action = "forceresync"
	data, _ := json.Marshal(m)
	send(roomid, deviceid,data)
}

type common_string_struct struct {
	Action string `json:"action"`
	Data   string `json:"data"`
}
type common_int_struct struct {
	Action string  `json:"action"`
	Data   int `json:"data"`
}
type common_float_struct struct {
	Action string  `json:"action"`
	Data   float64 `json:"data"`
}
type common_interface_struct struct {
	Action string                 `json:"action"`
	Data   map[string]interface{} `json:"data"`
}

func broadcast_seteffect(roomid int) {
	var m2 common_string_struct
	m2.Action = "seteffect"
	m2.Data = room(roomid).Effect
	data, _ := json.Marshal(m2)
	send_broadcast(roomid,data)
}

//////////////////////////////////////////////////
func broadcast_setspeed(roomid int) {
	var m2 common_float_struct
	m2.Action = "setspeed"
	m2.Data = room(roomid).Speed
	data, _ := json.Marshal(m2)
	send_broadcast(roomid,data)
}

//////////////////////////////////////////////////
func setbrightness(roomid int, deviceid int, brightness int) {
	var m2 common_int_struct
	m2.Action = "setbrightness"
	m2.Data = brightness
	data, _ := json.Marshal(m2)
	send(roomid, deviceid,data)
}
func broadcast_setbrightness(roomid int) {
	var m2 common_int_struct
	m2.Action = "setbrightness"
	m2.Data = room(roomid).Brightness
	data, _ := json.Marshal(m2)
	send_broadcast(roomid,data)
}

//////////////////////////////////////////////////
func broadcast_setbgblinkinterval(roomid int) {
	var m2 common_float_struct
	m2.Action = "setbgblinkinterval"
	m2.Data = room(roomid).Bgblinkinterval
	data, _ := json.Marshal(m2)
	send_broadcast(roomid,data)
}

//////////////////////////////////////////////////
func broadcast_settext(roomid int) {

	var m2 common_string_struct
	m2.Action = "settext"
	m2.Data = room(roomid).Text
	data, _ := json.Marshal(m2)
	send_broadcast(roomid,data)
}

//////////////////////////////////////////////////
func broadcast_settextsize(roomid int) {

	var m2 common_float_struct
	m2.Action = "settextsize"
	m2.Data = room(roomid).Textsize
	data, _ := json.Marshal(m2)
	send_broadcast(roomid,data)
}

//////////////////////////////////////////////////
func broadcast_updatepannelui(roomid int) {
	var m2 single_action_struct
	m2.Action = "updatepannelui"
	data, _ := json.Marshal(m2)
	send_broadcast(roomid,data)
}

//////////////////////////////////////////////////
func broadcast_setbgcolor(roomid int) {
	var m2 common_string_struct
	m2.Action = "setbackgroundcolor"
	m2.Data = room(roomid).Bgcolor
	data, _ := json.Marshal(m2)
	send_broadcast(roomid,data)
}

//////////////////////////////////////////////////
func broadcast_settextcolor(roomid int) {
	var m2 common_string_struct
	m2.Action = "settextcolor"
	m2.Data = room(roomid).Textcolor
	data, _ := json.Marshal(m2)
	send_broadcast(roomid,data)
}

/////////////////////////////////////////////////////
type createroom_struct struct {
	Success 	bool 	`json:"success"`
	Description string 	`json:"description"`
	Roomid 		int    	`json:"roomid"`
	Token  		string 	`json:"token"`
}

func api(w http.ResponseWriter, r *http.Request) {
	action := mux.Vars(r)["action"]
	/*v := r.URL.Query()
	roomtoken := v.Get("roomtoken")
	roomid, _ := strconv.Atoi(v.Get("roomid"))
	result := verify_roomtoken(roomid,roomtoken)
	if !result.Success {
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(&result)
		w.Write(data)
		return
	}*/
	log.Println("api gets called: ", action)
	switch action {

	case "createroom":
		token := KEY()
		roomcount++
		roomid := roomcount

		var room room_struct
		room.Text = "Milky Way Barrage"
		room.Speed = -10
		room.Brightness = 100
		room.Start_timestamp = timestamp()
		room.Textsize = 100
		room.Textcolor = "#FFFFFF"
		room.Bgcolor = "#000000"
		room.Effect = "bounce"
		room.Token = token
		room.Bgblinkinterval = 0
		room.Devicelist = make([]*device_struct, 0, 5)
		roomdic.Store(roomid, &room)
		log.Println("room created: ", room, roomid)

		var m createroom_struct
		m.Success = true
		m.Description = ""
		m.Roomid = roomid
		m.Token = token
		data, _ := json.Marshal(&m)
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
        room_cleanup(roomid)
        go func(){
            
                time.Sleep(time.Duration(2)*time.Second)
                broadcast_sendmsg(roomid,"更多功能仍在开发中")
                time.Sleep(time.Duration(2)*time.Second)
                broadcast_sendmsg(roomid,"如有建议，欢迎移步QQ群")
                time.Sleep(time.Duration(60)*time.Second)
            
        }()
		break
	case "debug":
		w.Header().Set("Content-Type", "application/json")
		data, _ := MarshalJSON(&roomdic)
		w.Write(data)
		break
	case "log":
		t, _ := ioutil.ReadFile("./log.txt")
		w.Write([]byte(t))
		break	
	default:
		w.Write([]byte("invaild method"))
	}

}
func privacy(w http.ResponseWriter, r *http.Request) {
	t, _ := ioutil.ReadFile("./static/privacy.txt")
	w.Write([]byte(t))
}
func index(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("mkdm server v0.1"))
}
//////////////////////////////////////////////

type room_struct struct {
	Text            string           `json:"text"`
	Speed           float64          `json:"speed"`
	Start_timestamp int          `json:"start_timestamp"`
	Textsize        float64          `json:"textsize"`
	Textcolor       string           `json:"textcolor"`
	Bgcolor         string           `json:"bgcolor"`
	Effect          string           `json:"effect"`
	Token           string           `json:"-"`
	Bgblinkinterval float64          `json:"bg_blink_interval"`
	Brightness      int         `json:"brightness"`
	Devicelist      []*device_struct `json:"devicelist"`
	Deviceindex     sync.Map

}
type device_struct struct {
	wsconn        *websocket.Conn
	cxt           context.Context
	Id            int     `json:"id"`
	Status        string  `json:"status"`
	Master        bool    `json:"master"`
	Brand         string  `json:"brand"`
	Model         string  `json:"model"`
	Platform      string  `json:"platform"`
	Safezone      float64 `json:"safezone"`
	Screenwidth   float64 `json:"screenwidth"`
	Screenheight  float64 `json:"screenheight"`
	Token         string  `json:"-"`
	Batterylevel  int     `json:"batterylevel"`
	Batterystatus string  `json:"batterystatus"`
	Nettype       string  `json:"nettype"`
}

var json = jsoniter.ConfigCompatibleWithStandardLibrary
var roomdic sync.Map
var roomcount = 0
var devicecount = 0

func room(roomid int) *room_struct {
	t, _ := roomdic.Load(roomid)
	return t.(*room_struct)
}
func MarshalJSON(f *sync.Map) ([]byte, error) {
	tmpMap := make(map[int]*room_struct)
	f.Range(func(k, v interface{}) bool {
		tmpMap[k.(int)] = v.(*room_struct)
		return true
	})
	return json.Marshal(tmpMap)
}

func main() {
	
	file := "./log.txt"
    logFile, _ := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0766)
    log.SetOutput(io.MultiWriter(logFile, os.Stdout))
    log.SetFlags(log.Ldate|log.Ltime)

	mux := mux.NewRouter()
	mux.HandleFunc("/ws/{version}/{roomid}/{roomtoken}", ws)
	mux.HandleFunc("/api/{action}", api)
	mux.HandleFunc("/privacy", privacy)
	mux.HandleFunc("/", index)
	log.Println(http.ListenAndServe("0.0.0.0:8000", handlers.RecoveryHandler()(mux)))

}

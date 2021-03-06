package main

import (
	"errors"
	//"github.com/Terry-Mao/gopush-cluster/hash"
	myrpc "github.com/Terry-Mao/gopush-cluster/rpc"
	"net"
	"net/http"
	"net/http/pprof"
	"net/rpc"
	"time"
)

const (
	// internal failed
	retInternalErr = 65535
	// param error
	retParamErr = 65534
	// ok
	retOK = 0
	// create channel failed
	retCreateChannelErr = 1
	// add channel failed
	retAddChannleErr = 2
	// get channel failed
	retGetChannelErr = 3
	// message push failed
	retPushMsgErr = 4
	// migrate failed
	retMigrateErr = 5
)

const (
	WebsocketProtocol = 0
	TCPProtocol       = 1
	heartbeatMsg      = "h"
	oneSecond         = int64(time.Second)
)

var (
	// Exceed the max subscriber per key
	MaxConnErr = errors.New("Exceed the max subscriber connection per key")
	// Assection type failed
	AssertTypeErr = errors.New("Subscriber assert type failed")
	// Auth token failed
	AuthTokenErr = errors.New("Auth token failed")
	// Token exists
	TokenExistErr = errors.New("Token already exist")

	// heartbeat bytes
	heartbeatBytes = []byte(heartbeatMsg)
	// heartbeat len
	heartbeatByteLen = len(heartbeatMsg)
)

// StartPprofHttp start http pprof
func StartPprofHttp() error {
	pprofServeMux := http.NewServeMux()
	pprofServeMux.HandleFunc("/debug/pprof/", pprof.Index)
	pprofServeMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	pprofServeMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	pprofServeMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)

	Log.Info("start listen pprof addr:%s", Conf.PprofAddr)
	err := http.ListenAndServe(Conf.PprofAddr, pprofServeMux)
	if err != nil {
		Log.Error("http.ListenAdServe(\"%s\") failed (%s)", Conf.PprofAddr, err.Error())
		return err
	}

	return nil
}

// StartRPC start accept rpc call
func StartRPC() error {
	c := &ChannelRPC{}
	rpc.Register(c)
	l, err := net.Listen("tcp", Conf.AdminAddr)
	if err != nil {
		Log.Error("net.Listen(\"tcp\", \"%s\") failed (%s)", Conf.AdminAddr, err.Error())
		return err
	}

	defer func() {
		if err := l.Close(); err != nil {
			Log.Error("listener.Close() failed (%s)", err.Error())
		}
	}()

	Log.Info("start listen admin addr:%s", Conf.AdminAddr)
	rpc.Accept(l)
	return nil
}

// Channel RPC
type ChannelRPC struct {
}

// New expored a method for creating new channel
func (c *ChannelRPC) New(key *string, ret *int) error {
	if *key == "" {
		Log.Warn("ChannelRPC New param error")
		*ret = retParamErr
		return nil
	}

	// create a new channel for the user
	Log.Info("user_key:\"%s\" add channel", *key)
	_, err := UserChannel.New(*key)
	if err != nil {
		Log.Error("user_key:\"%s\" can't create channle", *key)
		*ret = retCreateChannelErr

		return nil
	}

	*ret = retOK
	return nil
}

// Close expored a method for closing new channel
func (c *ChannelRPC) Close(key *string, ret *int) error {
	if *key == "" {
		Log.Warn("ChannelRPC Close param error")
		*ret = retParamErr
		return nil
	}

	// close the channle for the user
	Log.Info("user_key:\"%s\" close channel", *key)
	ch, err := UserChannel.Get(*key)
	if err != nil {
		Log.Error("user_key:\"%s\" can't get channle", *key)
		*ret = retGetChannelErr

		return nil
	}

	// ignore channel close error, only log a warnning
	if err = ch.Close(); err != nil {
		Log.Warn("user_key:\"%s\" can't close channel", *key)
	}

	*ret = retOK
	return nil
}

// Publish expored a method for publishing a message for the channel
func (c *ChannelRPC) Publish(m *myrpc.ChannelPublishArgs, ret *int) error {
	if m == nil || m.Key == "" || m.Msg == "" {
		Log.Warn("ChannelRPC Publish param error")
		*ret = retParamErr
		return nil
	}

	expire := m.Expire
	if expire <= 0 {
		expire = Conf.MessageExpireSec
	}

	// get a user channel
	ch, err := UserChannel.Get(m.Key)
	if err != nil {
		Log.Warn("user_key:\"%s\" can't get a channel (%s)", m.Key, err.Error())
		*ret = retGetChannelErr
		return nil
	}

	// TODO call rpc with web
	// use the channel push message
	if err = ch.PushMsg(&Message{Msg: m.Msg, Expire: time.Now().UnixNano() + expire*Second, MsgID: m.MsgID}, m.Key); err != nil {
		Log.Error("user_key:\"%s\" push message failed (%s)", m.Key, err.Error())
		*ret = retPushMsgErr
		MsgStat.IncrFailed()
		return nil
	}

	MsgStat.IncrSucceed()
	*ret = retOK
	return nil
}

/*
// MigrateHandle close Channel when node add or remove
func MigrateHandle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		Log.Warn("client:%s's %s not allowed", r.RemoteAddr, r.Method)
		http.Error(w, "Method Not Allowed", 405)
		return
	}

	// get params
	params := r.URL.Query()
	nodesStr := params.Get("nodes")
	nodes := strings.Split(nodesStr, ",")
	if len(nodes) == 0 {
		Log.Warn("client:%s's nodes param error", r.RemoteAddr)
		if err := retWrite(w, "nodes param error", retParamErr); err != nil {
			Log.Error("retWrite failed (%s)", err.Error())
		}

		return
	}

	vnodeStr := params.Get("vnode")
	vnode, err := strconv.Atoi(vnodeStr)
	if err != nil {
		Log.Error("strconv.Atoi(\"%s\") failed (%s)", vnodeStr, err.Error())
		if err = retWrite(w, "vnode param error", retParamErr); err != nil {
			Log.Error("retWrite failed (%s)", err.Error())
		}

		return
	}

	// check current node in the nodes
	has := false
	for _, str := range nodes {
		if str == Conf.Node {
			has = true
		}
	}

	if !has {
		Log.Crit("make sure your migrate nodes right, there is no %s in nodes, this will cause all the node hit miss", Conf.Node)
		if err = retWrite(w, "migrate nodes may be error", retMigrate); err != nil {
			Log.Error("retWrite failed (%s)", err.Error())
		}

		return
	}

	channels := []Channel{}
	// init ketama
	ketama := hash.NewKetama2(nodes, vnode)
	// get all the channel lock
	for i, c := range UserChannel.Channels {
		Log.Info("migrate channel bucket:%d", i)
		c.Lock()
		for k, v := range c.Data {
			hn := ketama.Node(k)
			if hn != Conf.Node {
				channels = append(channels, v)
				Log.Debug("migrate key:\"%s\" hit node:\"%s\"", k, hn)
			}
		}

		c.Unlock()
		Log.Info("migrate channel bucket:%d finished", i)
	}

	// close all the migrate channels
	Log.Info("close all the migrate channels")
	for _, channel := range channels {
		if err = channel.Close(); err != nil {
			Log.Error("channel.Close() failed (%s)", err.Error())
			continue
		}
	}

	Log.Info("close all the migrate channels finished")
	// ret response
	if err = retWrite(w, "ok", retOK); err != nil {
		Log.Error("retWrite() failed (%s)", err.Error())
		return
	}
}
*/

![images](http://wiki.jikexueyuan.com/project/nsq-guide/images/design5.png)

###  1：使用TCP协议获取

> [go-nsq](https://github.com/nsqio/go-nsq) 客户端发送消息：
```
V2

IDENTIFY

SUB is_test2 ch

RDY 1

RDY 1

FIN 07ee6e602a3fa000
```

> 当服务器收到客户端的请求后，会启动messagePump服务。这个服务有点像提前监听：先开启监听服务，至于监听谁你以后再告诉我。

> github.com/nsqio/nsq/nsqd/protocol_v2.go #206

#### V2

> github.com/nsqio/nsq/nsqd/tcp.go #33

> 版本号，用于优雅升级

#### IDENTIFY 

> github.com/nsqio/nsq/nsqd/protocol_v2.go #355

> 验证和初始化客户端通信信息

#### SUB is_test2 ch

> github.com/nsqio/nsq/nsqd/protocol_v2.go #589

```golang
func (p *protocolV2) SUB(client *clientV2, params [][]byte) ([]byte, error) {
    ......
    topic := p.ctx.nsqd.GetTopic(topicName)
    channel := topic.GetChannel(channelName)
    channel.AddClient(client.ID, client)
    
    atomic.StoreInt32(&client.State, stateSubscribed)
    client.Channel = channel
    // update message pump
    client.SubEventChan <- channel
    ......
}

```

> 确定将要处理的channel

> 触发 client.SubEventChan 事件，告诉messagePump服务需要监听的channel。

```
func (p *protocolV2) messagePump(client *clientV2, startedChan chan bool) {

    for {
        ......
    	select {
        ......
    	case subChannel = <-subEventChan:
    		// you can't SUB anymore
    		subEventChan = nil
        ......
    	}
    }
    ......
}
```

#### RDY 1  go-nsq客户端发送2次，应该是个BUG。

```
func (p *protocolV2) messagePump(client *clientV2, startedChan chan bool) {

    for {
    
        if subChannel == nil || !client.IsReadyForMessages() {
        .....
        } else if flushed {
            // last iteration we flushed...
            // do not select on the flusher ticker channel
            // 监听 channel
            memoryMsgChan = subChannel.memoryMsgChan
            backendMsgChan = subChannel.backend.ReadChan()
            flusherChan = nil
        } else {
            ......
        }
    
        ......
    	select {
    	......
    	//没有看出作用
    	case <-client.ReadyStateChan:
        ......
    	case msg := <-memoryMsgChan:
            if sampleRate > 0 && rand.Int31n(100) > sampleRate {
            	continue
            }
            msg.Attempts++
            
            subChannel.StartInFlightTimeout(msg, client.ID, msgTimeout)
            client.SendingMessage()
            err = p.SendMessage(client, msg, &buf)
            if err != nil {
            	goto exit
            }
            flushed = false
            ......
    	}
    }
    ......
}
```

> 经过SUB后，客户端会发送RDY告诉服务器，已准备好接收。服务器再从memoryMsgChan 获取数据，响应客户端同时将数据放入 inFlightMessages（消息担保）。

#### FIN 07ee6e602a3fa000 

> github.com/nsqio/nsq/nsqd/protocol_v2.go #667

> 07ee6e602a3fa000 为MessageId，根据这个MessageId删除inFlightMessages里的数据完成整个投送过程。

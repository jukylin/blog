### 1：开启出动推送

> nsq_to_http 和 nsqd通过 TCP 通讯

```
nsq_to_http --channel=ch --topic=test \
--post="http://api-coupon.etcchebao.com/coupon/test" \
--nsqd-tcp-address=127.0.0.1:4150 \
--content-type="application/x-www-form-urlencoded"
```



### 2：监听前准备工作
> 在监听前nsq_to_http会发送

V2
  
IDENTIFY
  
SUB test ch

RDY 200

> 确定要监听的主题，channel和暂接收200条消息

> 开启读和写事件

> github.com/nsqio/go-nsq/conn.go #181

```
func (c *Conn) Connect() (*IdentifyResponse, error) {
    ......
    c.wg.Add(2)
    atomic.StoreInt32(&c.readLoopRunning, 1)
    // 读取nsq发来的数据
    go c.readLoop()
    // 对nsq响应
    go c.writeLoop()
    return resp, nil
}

```

> 200个处理事件，把消息放送给订阅方

> github.com/nsqio/go-nsq/consumer.go #1079

```
func (r *Consumer) AddConcurrentHandlers(handler Handler, concurrency int) {
    if atomic.LoadInt32(&r.connectedFlag) == 1 {
        panic("already connected")
    }
    
    atomic.AddInt32(&r.runningHandlers, int32(concurrency))
    for i := 0; i < concurrency; i++ {
        go r.handlerLoop(handler)
    }
}

```

### 3：消息重试次数为 默认最多5次

> github.com/nsqio/go-nsq/consumer.go #1126

```
func (r *Consumer) shouldFailMessage(message *Message, handler interface{}) bool {
    // message passed the max number of attempts
    if r.config.MaxAttempts > 0 && message.Attempts > r.config.MaxAttempts {
        r.log(LogLevelWarning, "msg %s attempted %d times, giving up",
        	message.ID, message.Attempts)
        
        logger, ok := handler.(FailedMessageLogger)
        if ok {
            logger.LogFailedMessage(message)
        }
        
        return true
    }
    return false
}
```

> 超过重试次数会发送 “FIN 消息ID” 告诉nsqd把消息删除掉

> github.com/nsqio/go-nsq/conn.go #690

```
func (c *Conn) onMessageFinish(m *Message) {
    c.msgResponseChan <- &msgResponse{msg: m, cmd: Finish(m.ID), success: true}
}
```

### 4：主动推送的担保

> 通过HTTP状态实现

> github.com/nsqio/nsq/apps/nsq_to_http/nsq_to_http.go

```
func (p *PostPublisher) Publish(addr string, msg []byte) error {
    buf := bytes.NewBuffer(msg)
    resp, err := HTTPPost(addr, buf)
    if err != nil {
        return err
    }
    io.Copy(ioutil.Discard, resp.Body)
    resp.Body.Close()
    
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
    	return fmt.Errorf("got status code %d", resp.StatusCode)
    }
    return nil
}

```

> 如果订阅者返回 HTTP状态码 2XX，nsq_to_http 返回“FIN 消息ID”告诉nsqd消息接收成功，可从缓存区删除。

> 如果订阅者返回 HTTP状态码 非2XX，nsq_to_http返回 “REQ 消息ID”告诉nsqd，把消息从缓存区放回队列。



![image](http://static.oschina.net/uploads/img/201401/03081429_evAT.gif)

### 介绍nsq时常见的图。通过源码解剖它的实现。

#### messagePump 源码

> github.com/nsqio/nsq/nsqd/topic.go #215

```
 func (t *Topic) messagePump() {
    ......
    for {
    	select {
    	case msg = <-memoryMsgChan:
    ......
    	case <-t.exitChan:
    		goto exit
    	}

        for i, channel := range chans {
            chanMsg := msg
        
            // copy the message because each channel
            // needs a unique instance but...
            // fastpath to avoid copy if its the first channel
            // (the topic already created the first copy)
            if i > 0 {
            	chanMsg = NewMessage(msg.ID, msg.Body)
            	chanMsg.Timestamp = msg.Timestamp
            	chanMsg.deferred = msg.deferred
            }
            if chanMsg.deferred != 0 {
            	channel.PutMessageDeferred(chanMsg, chanMsg.deferred)
            	continue
            }
            err := channel.PutMessage(chanMsg)
            ......
        }
    }
    ......
}
```

#### 创建topic时，都会产生一个消息分发messagePump服务。

> github.com/nsqio/nsq/nsqd/topic.go #44

```
func NewTopic(topicName string, ctx *context, deleteCallback func(*Topic)) *Topic {
    t := &Topic{
    ......
    }
    ......
    t.waitGroup.Wrap(func() { t.messagePump() })
    t.ctx.nsqd.Notify(t)
    return t
}
```

#### 向topic发送消息时会触发一个memoryMsgChan事件。通过这个事件，消息被分发到topic的各个channel下。

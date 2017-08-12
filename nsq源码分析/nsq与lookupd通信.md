
### 1：基于TCP协议通讯，TCP长连接

### 2：心跳测试
> nsqd 和 lookupd 之间每隔15秒做一次心跳测试

```
func (n *NSQD) lookupLoop() {
    ......
    // for announcements, lookupd determines the host automatically
    ticker := time.Tick(15 * time.Second)
    for {
        ......
        select {
        case <-ticker:
        // send a heartbeat and read a response (read detects closed conns)
        for _, lookupPeer := range lookupPeers {
            n.logf("LOOKUPD(%s): sending heartbeat", lookupPeer)
            cmd := nsq.Ping()
            _, err := lookupPeer.Command(cmd)
            if err != nil {
            	n.logf("LOOKUPD(%s): ERROR %s - %s", lookupPeer, cmd, err)
            }
        }
        ......
        case <-n.exitChan:
        	goto exit
        }
    }
exit:
    n.logf("LOOKUP: closing")
}

```

> nsqd使用tcp 发送 ping + 客户端信息给lookupd。
> lookupd 返回 ok。完成心跳测试流程。

### 3：nsq 注册 lookupd
> 代码位置：nsqio\nsq\nsqd\lookup.go:31
第一次发送：
```golang
cmd{Name:IDENTIFY,Params:nil,
Body:{"broadcast_address":"apple.local",
"hostname":"apple.local","http_port":4151,
"tcp_port":4150,"version":"1.0.0-compat"}}
```

> nsq 注册时，lookupd 会以ip为下标 记录nsq客户端信息，信息放入 registrationMap 里。 

> github.com/nsqio/nsq/nsqlookupd/lookup_protocol_v1.go #227

```golang
client.peerInfo = &peerInfo
if p.ctx.nsqlookupd.DB.AddProducer(Registration{"client", "", ""}, &Producer{peerInfo: client.peerInfo}) {
    p.ctx.nsqlookupd.logf("DB: client(%s) REGISTER category:%s key:%s subkey:%s", client, "client", "", "")
}
```

> github.com/nsqio/nsq/nsqlookupd/registration_db.go  # 71

```golang
// add a producer to a registration
func (r *RegistrationDB) AddProducer(k Registration, p *Producer) bool {
	r.Lock()
	defer r.Unlock()
	producers := r.registrationMap[k]
	found := false
	for _, producer := range producers {
		if producer.peerInfo.id == p.peerInfo.id {
			found = true
		}
	}
	if found == false {
		r.registrationMap[k] = append(producers, p)
	}
	return !found
}
```

### 4：数据传输方式 使用json序列化，再通过 binary 包，把数据转为二进制。

## 接触到的包
#### 1：time

#### 2：reader

#### 3：binary

#### 4：atomic  善用原子操作，它会比锁更为高效
> [原子操作](https://github.com/polaris1119/The-Golang-Standard-Library-by-Example/blob/master/chapter16/16.02.md)

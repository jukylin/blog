> curl -d 'hello world' 'http://127.0.0.1:4151/put?topic=test'

### 为每条消息生成一个message Id
> github.com/nsqio/nsq/nsqd/http.go # 217


### message结构体信息
> github.com/nsqio/nsq/nsqd/message.go # 18

```golang
type Message struct {
	ID        MessageID
	Body      []byte
	Timestamp int64
	Attempts  uint16

	// for in-flight handling
	deliveryTS time.Time
	clientID   int64
	pri        int64
	index      int
	deferred   time.Duration
}
```

### 将消息放入memoryMsgChan 共享通道
```
func (t *Topic) put(m *Message) error {
    select {
    //当t.memoryMsgChan 满写不进，channel把数据写入文件
    case t.memoryMsgChan <- m:
    default:
    	b := bufferPoolGet()
    	err := writeMessageToBackend(b, m, t.backend)
    	bufferPoolPut(b)
    	t.ctx.nsqd.SetHealth(err)
    	if err != nil {
    	// TODO Error handle
        ...... 
    	}
    }
    return nil
}
```

> 默认放1W条数据，超过放入磁盘

> topic的backend实现在 github.com/nsqio/go-diskqueue/diskqueue.go

### 如何放入磁盘

> github.com/nsqio/nsq/nsqd/message.go # 40

#### 数据保存格式

![images](https://github.com/Aqiling/blog/blob/master/B11F3E5A-0BCE-4D3A-9F3C-157222D7FB45.png?raw=true)

```
binary.BigEndian.PutUint64(buf[:8], uint64(m.Timestamp))
binary.BigEndian.PutUint16(buf[8:10], uint16(m.Attempts))

n, err := w.Write(buf[:])
total += int64(n)
if err != nil {
	return total, err
}

n, err = w.Write(m.ID[:])
total += int64(n)
if err != nil {
	return total, err
}

n, err = w.Write(m.Body)
```
> 消息组成：将 Message 的Timestamp，Attempts，ID，Body, 再加上消息的长度len(len+Timestamp+Attempts+ID+Body)，组成一条消息。

> 消息与消息之间有4个字节的空格

> 当数据大于maxBytesPerFile(默认100M)，将会对文件进行切割。

> github.com/nsqio/go-diskqueue/diskqueue.go # 373

```
if d.writePos > d.maxBytesPerFile {
    d.writeFileNum++
    d.writePos = 0
    
    // sync every time we start writing to a new file
    // sync 里会把 writePost，readPos，writeFileNum等信息进行持久化
    err = d.sync()
    if err != nil {
    	d.logf("ERROR: diskqueue(%s) failed to sync - %s", d.name, err)
    }
    
    if d.writeFile != nil {
    	d.writeFile.Close()
    	d.writeFile = nil
    }
}
```

### writePos
> 作用：用于追加数据。记录文件最后偏移位置。退出nsq会持久化保存，开启会初始化。

### readPos 和writePos相对

### 接触的包

> [os.Sync](https://golang.org/pkg/os/#File.Sync)  
[go文件操作大全](https://gocn.io/article/40)

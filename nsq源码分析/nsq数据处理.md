> curl -d "" http://localhost:4151/topic/create?topic=is_test

### 1：创建topic

> 创建topic使用HTTP协议，与以往不同nsq使用装饰器处理，不是web开发常用的MVC模式
> github.com/nsqio/nsq/nsqd/http.go # 33

```
	router.Handle("POST", "/topic/create", http_api.Decorate(s.doCreateTopic, log, http_api.V1))

```

> 创建topic把 topicName 放入 topicMap内存里，同时把元数据放入文件


#### topic信息保存到文件

> github.com/nsqio/nsq/nsqd/nsqd.go #366

```
func (n *NSQD) PersistMetadata() error {
	// persist metadata about what topics/channels we have, across restarts
	fileName := newMetadataFile(n.getOpts())
	// old metadata filename with ID, maintained in parallel to enable roll-back
	fileNameID := oldMetadataFile(n.getOpts())

	n.logf("NSQ: persisting topic/channel metadata to %s", fileName)

        ......

	tmpFileName := fmt.Sprintf("%s.%d.tmp", fileName, rand.Int())

	err = writeSyncFile(tmpFileName, data)
	if err != nil {
		return err
	}
	err = os.Rename(tmpFileName, fileName)
	if err != nil {
		return err
	}
	// technically should fsync DataPath here

	stat, err := os.Lstat(fileNameID)
	if err == nil && stat.Mode()&os.ModeSymlink != 0 {
		return nil
	}

	// if no symlink (yet), race condition:
	// crash right here may cause next startup to see metadata conflict and abort

	tmpFileNameID := fmt.Sprintf("%s.%d.tmp", fileNameID, rand.Int())

	if runtime.GOOS != "windows" {
		err = os.Symlink(fileName, tmpFileNameID)
	} else {
		// on Windows need Administrator privs to Symlink
		// instead write copy every time
		err = writeSyncFile(tmpFileNameID, data)
	}


        ......
}
```

> 使用 json

> 保存方式先建临时文件保存，再把文件重命名。避免了数据在写入时突然中断。导致原文件不可用。


> [理解 Linux 的硬链接与软链接](https://www.ibm.com/developerworks/cn/linux/l-cn-hardandsymb-links/)

#### 触发了通知事件

>  topic信息放入n.notifyChan ，触发了主协程n.notifyChan事件，将topic信息注册到lookupd，在lookupd里topic信息被保存在registrationMap内存里


### 2：创建channel和topic方式大致一样。


> github.com/uber/jaeger/pkg/cache/lru.go

> Jaeger的lru算法主要由2部分组成
- byKey：map[string]*list.Element 保存数据用于 key/value 查询
- byAccess：container/list 双向链表 用于实现懒惰删除

## Get

```
func (c *LRU) Get(key string) interface{} {
	c.mux.Lock()
	defer c.mux.Unlock()

	elt := c.byKey[key]
	if elt == nil {
		return nil
	}

	cacheEntry := elt.Value.(*cacheEntry)
	if !cacheEntry.expiration.IsZero() && c.TimeNow().After(cacheEntry.expiration) {
		// Entry has expired
		if c.onEvict != nil {
			c.onEvict(cacheEntry.key, cacheEntry.value)
		}
		c.byAccess.Remove(elt)
		delete(c.byKey, cacheEntry.key)
		return nil
	}

	c.byAccess.MoveToFront(elt)
	return cacheEntry.value
}
```

> 通过key从map查询value
- 如果value过期把byKey和byAccess的数据都删除。
- 如果value没有过期就把value移到链表的最前面


## Put

```
func (c *LRU) putWithMutexHold(key string, value interface{}, elt *list.Element) interface{} {
    ......

	entry := &cacheEntry{
		key:   key,
		value: value,
	}

	if c.ttl != 0 {
		entry.expiration = c.TimeNow().Add(c.ttl)
	}
	c.byKey[key] = c.byAccess.PushFront(entry)
	for len(c.byKey) > c.maxSize {
		oldest := c.byAccess.Remove(c.byAccess.Back()).(*cacheEntry)
		if c.onEvict != nil {
			c.onEvict(oldest.key, oldest.value)
		}
		delete(c.byKey, oldest.key)
	}

	return nil
}
```

> 把key，value，expiration组成一个元素，并把元素放入链表的最前面
- 如果链表长度超出限制，则删除链表最后的元素

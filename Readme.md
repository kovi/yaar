# yaar

Very very simple artifact repository. It just supports POSTing artifacs, 
storing them on filesystem, adding some meta tags, and executing some triggers
when a new item is added.s

## Directory listing

With file sizes

## Tags

Assign meta data to items by adding `x-tag` headers.

## Triggers

Shell commands can be executed on item add and remove based on tags

## Locks

Items can have locks which prevents the item to be deleted. Currently only exclusive locks are available, 
which means that when a new item is added with the same lock the existing lock is removed and that item can be removed.

```sh
curl -X POST -H "x-lock: first-lock" --data-binary @test-file localhost:8080/filename -v
```

Locks can be removed by `meta` endpoint. eg. to remove all locks from `dir1/testfile`:
```sh
curl -X DELETE localhost:8080/meta/dir1/testfile/locks
```

## Todo
- [ ] upload size limit
- [ ] token auth
- [ ] audit log
- [ ] remove

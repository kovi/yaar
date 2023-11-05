# yaar

Very very simple artifact repository.
Supports POSTing artifacs, directory listing, meta tags, locking, and simple triggers.

<!-- TOC depthfrom:2 -->

- [Directory listing](#directory-listing)
    - [Ordering](#ordering)
    - [Filtering](#filtering)
- [Tags](#tags)
- [Triggers](#triggers)
- [Locks](#locks)
- [Todo](#todo)

<!-- /TOC -->

## Directory listing

With file sizes

### Ordering

The column can be selected as

- `c=m` by last-modified time
- `c=n` by name

Ordering direction can be selected as

- `o=d` desending
- `o=a` ascending

The query params are case sensitive.

Example to list by descending order of modification time

```sh
curl localhost:8080/?c=m&o=d
```

### Filtering

Can filter by

- `qn` - match name prefix
- `qt` - existence or matching value of tag
- `ql` - lock string existence

```sh
curl localhost:8080/?qt=tag
curl localhost:8080/?qt=tag=abc
curl localhost:8080/?ql=lockstr
```

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
- [ ] remove artifact

package auth

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type cachedUser struct {
	exists    bool
	isAdmin   bool
	expiresAt time.Time
}

type UserCache struct {
	mu    sync.RWMutex
	store map[uint]cachedUser
}

func NewUserCache() *UserCache {
	return &UserCache{
		store: make(map[uint]cachedUser),
	}
}

func (c *UserCache) Get(userID uint) (exists bool, isAdmin bool, found bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, ok := c.store[userID]
	if !ok || time.Now().After(item.expiresAt) {
		return false, false, false
	}
	return item.exists, item.isAdmin, true
}

func (c *UserCache) Set(userID uint, exists bool, isAdmin bool, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.store[userID] = cachedUser{
		exists:    exists,
		isAdmin:   isAdmin,
		expiresAt: time.Now().Add(ttl),
	}
}

// Invalidate allows us to force a re-check (e.g. after password reset)
func (c *UserCache) Invalidate(userID uint) {
	logrus.Infof("Invalidate user: %v", userID)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.store, userID)
}

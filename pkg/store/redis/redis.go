// Copyright © 2018 The Things Network Foundation, distributed under the MIT license (see LICENSE file)

package redis

import (
	"bytes"
	"io"
	"math/rand"
	"strings"
	"time"

	"github.com/TheThingsNetwork/ttn/pkg/config"
	"github.com/TheThingsNetwork/ttn/pkg/errors"
	"github.com/TheThingsNetwork/ttn/pkg/store"
	"github.com/TheThingsNetwork/ttn/pkg/util/randutil"
	"github.com/oklog/ulid"
	redis "gopkg.in/redis.v5"
)

const recursionLimit = 10

// Store represents a Redis store.Interface implemntation
type Store struct {
	Redis     *redis.Client
	config    *Config
	entropy   io.Reader
	indexKeys map[string]struct{}
}

// Config represents Redis configuration.
type Config struct {
	config.Redis
	IndexKeys []string
}

// New returns a new initialized Redis store.
func New(conf *Config) *Store {
	indexKeys := make(map[string]struct{}, len(conf.IndexKeys))
	for _, k := range conf.IndexKeys {
		indexKeys[k] = struct{}{}
	}
	return &Store{
		Redis: redis.NewClient(&redis.Options{
			Addr: conf.Address,
			DB:   conf.Database,
		}),
		config:    conf,
		entropy:   rand.New(randutil.NewLockedSource(rand.NewSource(time.Now().UnixNano()))),
		indexKeys: indexKeys,
	}
}

const separator = ":"

func (s *Store) key(str ...string) string {
	return s.config.Prefix + separator + strings.Join(str, separator)
}

func (s *Store) newID() fmt.Stringer {
	return ulid.MustNew(ulid.Now(), s.entropy)
}

func base(str string) string {
	ss := strings.Split(str, separator)
	return ss[len(ss)-1]
}

// Create stores generates an ULID and stores fields under a key associated with it.
func (s *Store) Create(fields map[string][]byte) (store.PrimaryKey, error) {
	fieldsSet := make(map[string]string, len(fields))
	idxAdd := make([]string, 0, len(fields))
	for k, v := range fields {
		str := string(v)
		fieldsSet[k] = str
		if _, ok := s.indexKeys[k]; ok {
			idxAdd = append(idxAdd, s.key(k, str))
		}
	}

	id := s.newID()
	if len(fields) == 0 {
		return id, nil
	}
	idStr := id.String()
	key := s.key(idStr)

	// recursion levels
	var n int
	var create func() error
	create = func() error {
		err := s.Redis.Watch(func(tx *redis.Tx) error {
			ok, err := tx.Exists(key).Result()
			if err != nil {
				return err
			}
			if ok {
				return errors.Errorf("A key %s already exists", idStr)
			}
			_, err = tx.Pipelined(func(p *redis.Pipeline) error {
				for _, k := range idxAdd {
					p.SAdd(k, idStr)
				}
				if len(fieldsSet) != 0 {
					p.HMSet(key, fieldsSet)
				}
				return nil
			})
			return err
		}, key)
		if n != recursionLimit && err == redis.TxFailedErr {
			return create()
		}
		return err
	}
	return id, create()
}

// Delete deletes the fields stored under the key associated with id.
func (s *Store) Delete(id store.PrimaryKey) (err error) {
	idStr := id.String()
	key := s.key(idStr)

	// recursion levels
	var n int
	var del func() error
	del = func() error {
		err = s.Redis.Watch(func(tx *redis.Tx) error {
			var idxCurrent []interface{}
			if len(s.config.IndexKeys) != 0 {
				typ, err := tx.Type(key).Result()
				if err != nil {
					return err
				}
				if typ == "hash" {
					idxCurrent, err = tx.HMGet(key, s.config.IndexKeys...).Result()
					if err != nil {
						return err
					}
				}
			}
			_, err = tx.Pipelined(func(p *redis.Pipeline) error {
				for i, curr := range idxCurrent {
					if curr != nil {
						p.SRem(s.key(s.config.IndexKeys[i], curr.(string)), idStr)
					}
				}
				p.Del(key)
				return nil
			})
			return err
		}, key)
		if n != recursionLimit && err == redis.TxFailedErr {
			return del()
		}
		return err
	}
	return del()
}

// Update overwrites field values stored under PrimaryKey specified with values in diff and rebinds indexed keys present in diff.
func (s *Store) Update(id store.PrimaryKey, diff map[string][]byte) (err error) {
	if len(diff) == 0 {
		return nil
	}

	idxDel := make([]string, 0, len(diff))
	idxAdd := make([]string, 0, len(diff))
	fieldsDel := make([]string, 0, len(diff))
	fieldsSet := make(map[string]string, len(diff))

	for k, v := range diff {
		_, isIndex := s.indexKeys[k]
		if isIndex {
			idxDel = append(idxDel, k)
		}

		fieldsDel = append(fieldsDel, k)
		if v == nil {
			continue
		}

		str := string(v)
		fieldsSet[k] = str
		if isIndex {
			idxAdd = append(idxAdd, s.key(k, str))
		}
	}

	idStr := id.String()
	key := s.key(idStr)

	// recursion levels
	var n int
	var update func() error
	update = func() error {
		err = s.Redis.Watch(func(tx *redis.Tx) error {
			var idxCurrent []interface{}
			if len(idxDel) != 0 {
				idxCurrent, err = tx.HMGet(key, idxDel...).Result()
				if err != nil {
					return err
				}
			}

			fieldsCurrent, err := tx.HKeys(key).Result()
			if err != nil {
				return err
			}

			m := make(map[string]struct{}, len(fieldsCurrent))
			for _, k := range fieldsCurrent {
				m[k] = struct{}{}
			}

			for _, fd := range fieldsDel {
				p := fd + store.Separator
				for fc := range m {
					if strings.HasPrefix(fc, p) {
						fieldsDel = append(fieldsDel, fc)
						delete(m, fc)
					}
				}
			}

			_, err = tx.Pipelined(func(p *redis.Pipeline) error {
				for i, k := range idxDel {
					if curr := idxCurrent[i]; curr != nil {
						p.SRem(s.key(k, curr.(string)), idStr)
					}
				}
				for _, k := range idxAdd {
					p.SAdd(k, idStr)
				}
				if len(fieldsDel) != 0 {
					p.HDel(key, fieldsDel...)
				}
				if len(fieldsSet) != 0 {
					p.HMSet(key, fieldsSet)
				}
				return nil
			})
			return err
		}, key)
		if n != recursionLimit && err == redis.TxFailedErr {
			return update()
		}
		return err
	}
	return update()
}

type stringBytesMapCmd struct {
	*redis.StringStringMapCmd
}

func (c *stringBytesMapCmd) Result() (map[string][]byte, error) {
	fields, err := c.StringStringMapCmd.Result()
	if err != nil {
		return nil, err
	}
	if fields == nil || len(fields) == 0 {
		return nil, nil
	}

	out := make(map[string][]byte, len(fields))
	for k, v := range fields {
		out[k] = []byte(v)
	}
	return out, nil
}

func newStringBytesMapCmd(c *redis.StringStringMapCmd) *stringBytesMapCmd {
	return &stringBytesMapCmd{c}
}

// Find returns the fields stored under PrimaryKey specified.
func (s *Store) Find(id store.PrimaryKey) (map[string][]byte, error) {
	m, err := newStringBytesMapCmd(s.Redis.HGetAll(s.key(id.String()))).Result()
	if err != nil {
		return nil, err
	}
	return m, nil
}

// FindBy returns mapping of PrimaryKey -> fields, which match field values specified in filter. Filter represents an AND relation,
// meaning that only entries matching all the fields in filter should be returned.
func (s *Store) FindBy(filter map[string][]byte) (out map[store.PrimaryKey]map[string][]byte, err error) {
	if len(filter) == 0 {
		return nil, nil
	}

	idxKeys := make([]string, 0, len(filter))
	fieldFilter := make([]string, 0, len(filter))
	for k, v := range filter {
		if _, ok := s.indexKeys[k]; ok {
			idxKeys = append(idxKeys, s.key(k, string(v)))
		} else {
			fieldFilter = append(fieldFilter, k)
		}
	}

	// recursion levels
	var n int
	var find func() error
	find = func() error {
		err := s.Redis.Watch(func(tx *redis.Tx) error {
			var ids []string
			if len(idxKeys) != 0 {
				ids, err = tx.SInter(idxKeys...).Result()
			} else {
				ids, err = tx.Keys(s.key("*")).Result()
				for i, key := range ids {
					ids[i] = base(key)
				}
			}
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				return nil
			}

			cmds := make(map[ulid.ULID]*stringBytesMapCmd, len(ids))
			_, err = tx.Pipelined(func(p *redis.Pipeline) error {
				for _, str := range ids {
					id, err := ulid.Parse(str)
					if err != nil {
						return errors.NewWithCausef(err, "Failed to parse %s as ULID, database inconsistent", str)
					}
					cmds[id] = newStringBytesMapCmd(p.HGetAll(s.key(str)))
				}
				return nil
			})
			if err != nil {
				return err
			}

			out = make(map[store.PrimaryKey]map[string][]byte, len(cmds))

		outer:
			for id, cmd := range cmds {
				m, err := cmd.Result()
				if err != nil {
					return err
				}
				if len(m) == 0 {
					continue
				}
				for _, k := range fieldFilter {
					if !bytes.Equal(m[k], filter[k]) {
						continue outer
					}
				}
				out[id] = m
			}
			return nil
		}, idxKeys...)
		if n != recursionLimit && err == redis.TxFailedErr {
			return find()
		}
		return err
	}
	return out, find()
}

var ErrNoValues = errors.New("No values specified")

func (s *Store) put(id store.PrimaryKey, bs ...[]byte) error {
	idStr := id.String()
	_, err := s.Redis.Pipelined(func(p *redis.Pipeline) error {
		for _, b := range bs {
			p.SAdd(idStr, b)
		}
		return nil
	})
	return err
}

func (s *Store) Put(id store.PrimaryKey, bs ...[]byte) error {
	if len(bs) == 0 {
		return errors.New("No fields specified")
	}
	return s.put(id, bs...)
}

func (s *Store) CreateSet(bs ...[]byte) (store.PrimaryKey, error) {
	id := s.newID()
	if len(bs) == 0 {
		return id, nil
	}

	if err := s.put(id, bs...); err != nil {
		return nil, err
	}
	return id, nil
}

func (s *Store) FindSet(id store.PrimaryKey) (bs [][]byte, err error) {
	return bs, s.Redis.SMembers(id.String()).ScanSlice(&bs)
}

func (s *Store) Contains(id store.PrimaryKey, b []byte) (bool, error) {
	return s.Redis.SIsMember(id.String(), b).Result()
}

func (s *Store) Remove(id store.PrimaryKey, bs ...[]byte) error {
	if len(bs) == 0 {
		return nil
	}
	idStr := id.String()
	_, err := s.Redis.Pipelined(func(p *redis.Pipeline) error {
		for _, b := range bs {
			p.SRem(idStr, b)
		}
		return nil
	})
	return err
}

func (s *Store) Append(id store.PrimaryKey, bs ...[]byte) error {
	n, err := s.Redis.RPush(id.String(), bs).Result()
	if err != nil {
		return err
	}
	if n < int64(len(bs)) {
		return errors.Errorf("Expected to store %d values, stored %d", len(bs), n)
	}
	return nil
}

func (s *Store) CreateList(bs ...[]byte) (store.PrimaryKey, error) {
	id := s.newID()
	if len(bs) == 0 {
		return id, nil
	}

	if err := s.Append(id, bs...); err != nil {
		return nil, err
	}
	return id, nil
}

func (s *Store) FindList(id store.PrimaryKey) (bs [][]byte, err error) {
	return bs, s.Redis.LRange(id.String(), 0, -1).ScanSlice(bs)
}

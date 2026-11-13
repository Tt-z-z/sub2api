// Package requestapproval implements the opt-in test-environment admission gate.
package requestapproval

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/redis/go-redis/v9"
	"strconv"
)

var ErrClosed = errors.New("approval unavailable or no longer pending")

type Entry struct {
	ID       string `json:"id"`
	Owner    string `json:"owner,omitempty"`
	State    string `json:"state"`
	Deadline int64  `json:"deadline"`
	Lease    int64  `json:"lease,omitempty"`
	Method   string `json:"method"`
	Route    string `json:"route"`
	Bytes    int    `json:"bytes"`
	KeyID    int64  `json:"key_id"`
	Preview  string `json:"preview,omitempty"`
	Raw      string `json:"raw,omitempty"`
	Text     string `json:"text,omitempty"`
}

type Store struct {
	Client    redis.UniversalClient
	Namespace string
}

// All operations share one hash-slot and use Redis time, never browser time.
// Queue metadata stays structural. A short-lived preview lives in a sibling hash
// and is deleted with the entry; the audit stream never receives it.
var transition = redis.NewScript(`
local t=redis.call('TIME'); local now=t[1]*1000+math.floor(t[2]/1000)
local op=ARGV[1]; local id=ARGV[2]; local owner=ARGV[3]
local all=redis.call('HGETALL',KEYS[1])
for i=1,#all,2 do
 local e=cjson.decode(all[i+1])
 if e.deadline<=now or e.lease<=now then redis.call('HDEL',KEYS[1],all[i]); redis.call('HDEL',KEYS[3],all[i]); redis.call('HDEL',KEYS[4],all[i]) end
end
if op=='list' then return redis.call('HVALS',KEYS[1]) end
if op=='reserve' then
 if redis.call('HLEN',KEYS[1])>=16 then return '' end
 local e=cjson.decode(ARGV[4]); e.deadline=now+180000; e.lease=now+3000; e.state='reading'
 redis.call('HSET',KEYS[1],id,cjson.encode(e)); redis.call('PEXPIRE',KEYS[1],181000)
 return cjson.encode(e)
end
local raw=redis.call('HGET',KEYS[1],id); if not raw then return '' end
local e=cjson.decode(raw)
if op=='approve' or op=='reject' then
 if e.state~='pending' then return '' end
 local actor=tonumber(ARGV[4]); if not actor or actor<=0 then return '' end
 -- Write the bounded audit first: an audit failure must never grant approval.
 redis.call('XADD',KEYS[2],'MAXLEN','=',10000,'*','request_id',id,'key_id',tostring(e.key_id),'admin_id',ARGV[4],'result',op)
 e.state=op=='approve' and 'approved' or 'rejected'
 if op=='approve' and type(ARGV[5])=='string' and ARGV[5]~='' then redis.call('HSET',KEYS[4],id,ARGV[5]); redis.call('PEXPIRE',KEYS[4],181000) end
elseif e.owner~=owner then return ''
elseif op=='cancel' then redis.call('HDEL',KEYS[1],id); redis.call('HDEL',KEYS[3],id); redis.call('HDEL',KEYS[4],id); return 'ok'
elseif op=='renew' then e.lease=now+3000
elseif op=='ready' then
 if e.state~='reading' then return '' end
 e.bytes=tonumber(ARGV[4]); e.state='pending'
 if type(ARGV[5])=='string' and ARGV[5]~='' then redis.call('HSET',KEYS[3],id,ARGV[5]); redis.call('PEXPIRE',KEYS[3],181000) end
elseif op=='consume' then
 if e.state~='approved' then return '' end
 redis.call('HDEL',KEYS[1],id); redis.call('HDEL',KEYS[3],id); redis.call('HDEL',KEYS[4],id); return 'ok'
else return '' end
redis.call('HSET',KEYS[1],id,cjson.encode(e)); return cjson.encode(e)
`)

func (s *Store) keys() []string {
	prefix := "request-approval:{" + s.Namespace + "}"
	return []string{prefix + ":queue:v1", prefix + ":audit:v1", prefix + ":preview:v1", prefix + ":rewrite:v1"}
}

func (s *Store) call(ctx context.Context, op, id, owner, data, extra string) (string, error) {
	v, err := transition.Run(ctx, s.Client, s.keys(), op, id, owner, data, extra).Text()
	if err != nil || v == "" {
		return "", ErrClosed
	}
	return v, nil
}
func (s *Store) Reserve(ctx context.Context, e Entry) (Entry, error) {
	e.Preview = ""
	b, _ := json.Marshal(e)
	v, err := s.call(ctx, "reserve", e.ID, e.Owner, string(b), "")
	if err == nil {
		err = json.Unmarshal([]byte(v), &e)
	}
	return e, err
}
func (s *Store) Ready(ctx context.Context, e Entry, n int, preview string) error {
	_, err := s.call(ctx, "ready", e.ID, e.Owner, strconv.Itoa(n), preview)
	return err
}
func (s *Store) Renew(ctx context.Context, e Entry) (Entry, error) {
	v, err := s.call(ctx, "renew", e.ID, e.Owner, "", "")
	if err == nil {
		err = json.Unmarshal([]byte(v), &e)
	}
	return e, err
}
func (s *Store) Decide(ctx context.Context, op, id string, adminID int64) error {
	return s.DecideRewrite(ctx, op, id, adminID, "")
}

// DecideRewrite records an approval. rewrite is a non-empty JSON payload only when
// the admin changed the displayed user text; it never enters the audit stream.
func (s *Store) DecideRewrite(ctx context.Context, op, id string, adminID int64, rewrite string) error {
	if op != "approve" && op != "reject" {
		return ErrClosed
	}
	if op != "approve" {
		rewrite = ""
	}
	_, err := s.call(ctx, op, id, "", strconv.FormatInt(adminID, 10), rewrite)
	return err
}

func (s *Store) ReadRewrite(ctx context.Context, id string) (string, error) {
	v, err := s.Client.HGet(ctx, s.keys()[3], id).Result()
	if err == redis.Nil {
		return "", nil
	}
	return v, err
}

func (s *Store) Action(ctx context.Context, op, id, owner string) error {
	_, err := s.call(ctx, op, id, owner, "", "")
	return err
}
func (s *Store) List(ctx context.Context) ([]Entry, error) {
	vals, err := transition.Run(ctx, s.Client, s.keys(), "list", "", "", "", "").StringSlice()
	if err != nil {
		return nil, ErrClosed
	}
	out := make([]Entry, 0, len(vals))
	for _, v := range vals {
		var e Entry
		if json.Unmarshal([]byte(v), &e) != nil {
			return nil, ErrClosed
		}
		e.Owner = ""
		e.Lease = 0
		e.Preview = ""
		e.Raw = ""
		e.Text = ""
		if e.State != "reading" {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return out, nil
	}
	ids := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	raw, err := s.Client.HMGet(ctx, s.keys()[2], ids...).Result()
	if err != nil {
		return nil, ErrClosed
	}
	for i, v := range raw {
		if text, ok := v.(string); ok {
			out[i].Preview, out[i].Raw, out[i].Text = splitStoredPreview(text)
		}
	}
	return out, nil
}

func splitStoredPreview(stored string) (string, string, string) {
	var packed struct {
		User string `json:"user"`
		Raw  string `json:"raw"`
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(stored), &packed) == nil && (packed.User != "" || packed.Raw != "" || packed.Text != "") {
		return packed.User, packed.Raw, packed.Text
	}
	return stored, "", ""
}

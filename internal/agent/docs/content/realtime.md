# Realtime (change events)

Subscribe to live row-change events over **Server-Sent Events (SSE)**. Every
insert, update, and delete to a user table is published; you receive an event
**only for rows you are authorized to read** — the same select policies that
govern `GET` are applied to each event, per subscriber.

## Subscribe

```
GET /v1/realtime            # all tables you can see
GET /v1/realtime?table=todos  # one table
```

The response is an event stream (`Content-Type: text/event-stream`). Each event:

```
event: insert
data: {"type":"insert","table":"todos","id":"…","row":{…},"at":"…"}
```

`type` is `insert`, `update`, or `delete`. `row` carries the row's values for
insert/update and the deleted row for delete (used for authorization). The server
sends `: ping` comments periodically to keep the connection alive.

## Authorization

Delivery is **deny-by-default**, filtered per subscriber:

- `project_admin` receives all events.
- Any other caller receives an event only if a `select` policy's `using`
  expression accepts that row for their role — identical to what `GET
  /v1/tables/{t}/records` would return. A private row never appears in another
  user's stream.

## Example (browser)

```js
const es = new EventSource("/v1/realtime?table=todos", { withCredentials: true });
es.onmessage = (m) => console.log(JSON.parse(m.data));
es.addEventListener("insert", (m) => render(JSON.parse(m.data).row));
```

> Realtime is best-effort: events are delivered to connected subscribers, not
> replayed from history. For a durable log, read the table.

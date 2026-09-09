import sqlite3, json

conn = sqlite3.connect('/home/ubuntu/beszel/beszel_data/data.db')
cur = conn.cursor()
cur.execute("SELECT id, name, users FROM systems")
rows = cur.fetchall()
print("systems:", rows)
for sys_id, name, users_json in rows:
    try:
        u_list = json.loads(users_json) if users_json else []
    except:
        u_list = []
    if "prevadm12345678" not in u_list:
        u_list.append("prevadm12345678")
        cur.execute("UPDATE systems SET users = ? WHERE id = ?", (json.dumps(u_list), sys_id))
        print(f"Added preview user to system {name} ({sys_id})")
conn.commit()

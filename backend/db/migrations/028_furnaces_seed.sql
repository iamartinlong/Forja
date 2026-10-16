-- 028 默认炉种子：forja-default 禁删自愈，一炉一容器 capacity=1
INSERT INTO furnaces(id, name, base_url, capacity, status, labels, project_id)
VALUES('forja-default','forja-default','http://forja-default-furnace:4096',1,'healthy','[]',NULL)
ON CONFLICT(id) DO UPDATE SET base_url=EXCLUDED.base_url, capacity=EXCLUDED.capacity;
-- 旧种子兼容：若曾以 name=default-furnace 写入，则归一
UPDATE furnaces SET id='forja-default', name='forja-default' WHERE name='default-furnace' AND id!='forja-default';

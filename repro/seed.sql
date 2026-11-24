-- A fictional show: two specials, and seasons 1 to 12 of three episodes each.
-- Run against a stopped instance's streamline.db.
INSERT INTO tv_shows(id,create_time,update_time,title,original_title,year,first_aired,overview,series_status,type,monitored,tvdb_id,runtime,aliases,network)
VALUES(1,'2026-01-01 00:00:00+00:00','2026-01-01 00:00:00+00:00','Harbour Lights','',2009,'2009-01-05 00:00:00+00:00','A lighthouse crew keeps the coast open.','ended','standard',1,900001,50,'[]','Demo TV');
INSERT INTO seasons(id,create_time,update_time,number,name,monitored,monitor_pinned,tv_show_seasons)
VALUES(100,'2026-01-01 00:00:00+00:00','2026-01-01 00:00:00+00:00',0,'Specials',1,0,1);
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<12)
INSERT INTO seasons(id,create_time,update_time,number,name,monitored,monitor_pinned,tv_show_seasons)
SELECT 100+i,'2026-01-01 00:00:00+00:00','2026-01-01 00:00:00+00:00',i,'',0,0,1 FROM n;
INSERT INTO episodes(id,create_time,update_time,number,title,air_date,monitored,monitor_pinned,status,season_episodes) VALUES
(1,'2026-01-01 00:00:00+00:00','2026-01-01 00:00:00+00:00',1,'The Lantern Night','2010-12-25 00:00:00+00:00',1,0,'wanted',100),
(2,'2026-01-01 00:00:00+00:00','2026-01-01 00:00:00+00:00',2,'Storm Over Blackrock','2013-11-23 00:00:00+00:00',1,0,'wanted',100);
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<12), e(j) AS (VALUES(1),(2),(3))
INSERT INTO episodes(id,create_time,update_time,number,title,air_date,monitored,monitor_pinned,status,season_episodes)
SELECT 100*i+j,'2026-01-01 00:00:00+00:00','2026-01-01 00:00:00+00:00',j,'Chapter '||j,(2008+i)||'-03-0'||j||' 00:00:00+00:00',0,0,'wanted',100+i FROM n,e;

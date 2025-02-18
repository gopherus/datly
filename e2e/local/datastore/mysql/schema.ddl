DROP TABLE IF EXISTS USER;
CREATE TABLE USER (
    ID         INT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    NAME       VARCHAR(255),
    MGR_ID     INT,
    ACCOUNT_ID INT
);

DROP TABLE IF EXISTS VENDOR;
CREATE TABLE VENDOR (
    ID           INT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    NAME         VARCHAR(255),
    ACCOUNT_ID   INT,
    CREATED      DATETIME,
    USER_CREATED INT,
    UPDATED      DATETIME,
    USER_UPDATED INT
);

DROP TABLE IF EXISTS PRODUCT;

CREATE TABLE PRODUCT (
    ID           INT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    NAME         VARCHAR(255),
    VENDOR_ID    INT,
    STATUS       INT,
    CREATED      DATETIME,
    USER_CREATED INT,
    UPDATED      DATETIME,
    USER_UPDATED INT
);

DROP TABLE IF EXISTS PRODUCT_JN;

CREATE TABLE PRODUCT_JN (
    PRODUCT_ID INT NOT NULL,
    USER_ID    INT,
    OLD_VALUE  VARCHAR(255),
    NEW_VALUE  VARCHAR(255),
    CREATED    DATETIME
);

DROP FUNCTION IF EXISTS IS_VENDOR_AUTHORIZED;

DELIMITER $$
CREATE FUNCTION IS_VENDOR_AUTHORIZED(USER_ID INT, VENDOR_ID INT)
    RETURNS BOOLEAN
BEGIN
    DECLARE
IS_AUTH BOOLEAN;
SELECT TRUE
INTO IS_AUTH
FROM VENDOR v
WHERE ID = VENDOR_ID
  AND ACCOUNT_ID
  AND EXISTS(SELECT 1 FROM USER u WHERE u.ID = USER_ID AND u.ACCOUNT_ID = v.ACCOUNT_ID);
RETURN IS_AUTH;
END $$
DELIMITER;


DROP FUNCTION IF EXISTS IS_PRODUCT_AUTHORIZED;

DELIMITER $$
CREATE FUNCTION IS_PRODUCT_AUTHORIZED(USER_ID INT, PID INT)
    RETURNS BOOLEAN
BEGIN
    DECLARE
IS_AUTH BOOLEAN;
    SET
IS_AUTH = FALSE ;
SELECT TRUE
INTO IS_AUTH
FROM VENDOR v
         JOIN PRODUCT p ON v.ID = p.VENDOR_ID
WHERE p.ID = PID
  AND ACCOUNT_ID
  AND EXISTS(SELECT 1
             FROM USER u
             WHERE u.ID = USER_ID
               AND u.ACCOUNT_ID = v.ACCOUNT_ID);
RETURN IS_AUTH;
END $$
DELIMITER;


DROP TABLE IF EXISTS DISTRICT;
CREATE TABLE DISTRICT (
    ID   INT PRIMARY KEY,
    NAME VARCHAR(255)
);

DROP TABLE IF EXISTS CITY;
CREATE TABLE CITY (
    ID          INT PRIMARY KEY,
    NAME        varchar(255),
    ZIP_CODE    varchar(255),
    DISTRICT_ID INT
);

DROP TABLE IF EXISTS TEAM;
CREATE TABLE TEAM (
    ID   INT PRIMARY KEY,
    NAME varchar(255),
    ACTIVE INTEGER
);

DROP TABLE IF EXISTS USER_TEAM;
CREATE TABLE USER_TEAM (
    ID      INT PRIMARY KEY,
    USER_ID INT,
    TEAM_ID INT
);

DROP TABLE IF EXISTS EVENTS;
CREATE TABLE EVENTS (
    ID INT AUTO_INCREMENT PRIMARY KEY,
    NAME varchar(255),
    QUANTITY INT
);

DROP TABLE IF EXISTS EVENTS_PERFORMANCE;
CREATE TABLE EVENTS_PERFORMANCE
(
    ID        INT AUTO_INCREMENT PRIMARY KEY,
    PRICE     INT,
    EVENT_ID  INT,
    TIMESTAMP DATE,
    FOREIGN KEY (EVENT_ID) REFERENCES EVENTS (ID)
);
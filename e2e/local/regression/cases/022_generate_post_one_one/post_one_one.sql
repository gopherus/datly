/* {"URI": "basic/events-one-one", "Method": "POST",
   "ResponseBody": {
        "StateValue": "Events"
   }
} */

SELECT EVENTS.*,
       EVENTS_PERFORMANCE.*
FROM (SELECT ID, QUANTITY FROM EVENTS) EVENTS
JOIN (SELECT * FROM EVENTS_PERFORMANCE) EVENTS_PERFORMANCE ON EVENTS.ID = EVENTS_PERFORMANCE.EVENT_ID
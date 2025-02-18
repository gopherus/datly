/*
{
   "URI":"basic/events-velty-transform",
   "Method":"POST",
   "Declare":{"Events":"*Events"},
   "TypeSrc":{
        "URL":"regression/cases/026_transform_service",
        "Types":["PerformanceData", "Events"]
   },
   "RequestBody":{
        "DataType": "Events"
   },
    "ResponseBody": { "From": "Events" }
} */

$sequencer.Allocate("EVENTS", $Events, "Id")
$sequencer.Allocate("EVENTS_PERFORMANCE", $Events, "EventsPerformance/Id")

#set($eTypes = $Events.TransformWithURL("http://localhost:8871/transform/events-perf", "[]*PerformanceData"))

#set($validationResult = $http.Do("POST", "http://localhost:8871/dev/validate/event-perf", $eTypes))
#if($validationResult.Invalid)
$logger.Fatal("%v", $validationResult.Message)
#end

INSERT INTO EVENTS (
    ID,
    QUANTITY
) VALUES (
    $Events.Id /* {"DataType":"Events","Target":"","Cardinality":"One"} */ ,
    $Events.Quantity
);

#foreach($recEventsPerformance in $Unsafe.Events.EventsPerformance)
	#set($recEventsPerformance.EventId = $Unsafe.Events.Id)
	INSERT INTO EVENTS_PERFORMANCE (
        ID,
        PRICE,
        EVENT_ID,
        TIMESTAMP
	) VALUES (
        $recEventsPerformance.Id,
        $recEventsPerformance.Price,
        $recEventsPerformance.EventId,
        $recEventsPerformance.Timestamp
	);
	
#end
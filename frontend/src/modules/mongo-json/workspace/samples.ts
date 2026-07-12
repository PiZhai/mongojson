export const mongoSample = `{
  "_id" : ObjectId("69c364c67ab3eb212eaf507d"),
  "bundleId" : "B001407",
  "bundleName" : "游泳健身权益",
  "conditional" : {
    "rules" : [
      {
        "method" : "ConditionalRule",
        "params" : [
          {
            "refreshCycle" : {
              "type" : "M"
            },
            "limit" : 1.0
          }
        ]
      }
    ]
  }
}`

export const diffSampleRight = `{
  "bundleName" : "游泳健身权益",
  "bundleId" : "B001407",
  "_id" : ObjectId("69c364c67ab3eb212eaf507d"),
  "conditional" : {
    "rules" : [
      {
        "method" : "ConditionalRule",
        "params" : [
          {
            "limit" : 2.0,
            "refreshCycle" : {
              "type" : "W"
            }
          }
        ]
      }
    ]
  }
}`

export const shellSample = `db.users.updateOne(
  { _id: ObjectId("69c364c67ab3eb212eaf507d") },
  { $set: { status: "active", score: 99 } }
)`

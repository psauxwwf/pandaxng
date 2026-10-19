return await search({
  rows: "#web-results .snippet, .snippet[data-type='web'], .snippet[data-type='web'] .snippet-body",
  links: ["a.heading-serpresult", "a:has(.title)", "a:has(h3)"],
  titles: [".title", "h3"], snippets: [".snippet-description", ".snippet-content", ".description"],
  ads: "[data-type='ad'], .ad", empty: ["No results found", "We couldn't find any results"]
});

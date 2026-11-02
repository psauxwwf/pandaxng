return await search({
  rows: "#b_results > .b_algo",
  links: ["h2 a"],
  titles: ["h2"],
  snippets: [".b_caption p", ".b_caption .b_snippet", ".b_snippet", ".b_algoSlug"],
  ads: ".b_ad, .b_adTop, .b_adBottom, [data-text-ad]",
  challenges: "#b_captcha, #b_captcha_container, iframe[src*='captcha']",
  empty: ["There are no results for", "No results found for", "We did not find any results for"],
});

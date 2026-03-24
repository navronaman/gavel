package fetcher

// SeedSources is the registry of RSS/Atom feed URLs polled by the WorkerPool.
var SeedSources = []string{
	// Reuters
	"https://feeds.reuters.com/reuters/worldNews",
	"https://feeds.reuters.com/Reuters/PoliticsNews",
	"https://feeds.reuters.com/reuters/technologyNews",

	// BBC
	"https://feeds.bbci.co.uk/news/world/rss.xml",
	"https://feeds.bbci.co.uk/news/politics/rss.xml",
	"https://feeds.bbci.co.uk/news/business/rss.xml",

	// AP News
	"https://rsshub.app/apnews/topics/apf-topnews",
	"https://rsshub.app/apnews/topics/apf-intlnews",

	// UN
	"https://news.un.org/feed/subscribe/en/news/all/rss.xml",
	"https://press.un.org/en/rss.xml",

	// US Government
	"https://www.state.gov/rss-feeds/press-releases/",
	"https://www.whitehouse.gov/feed/",
	"https://www.govtrack.us/events/events.rss?feeds=bill-introduced",
	"https://www.govtrack.us/events/events.rss?feeds=bill-status-change",

	// Al Jazeera
	"https://www.aljazeera.com/xml/rss/all.xml",
	"https://www.aljazeera.com/xml/rss/politics.xml",

	// Deutsche Welle
	"https://rss.dw.com/rdf/rss-en-all",
	"https://rss.dw.com/rdf/rss-en-top",

	// NPR
	"https://feeds.npr.org/1001/rss.xml",
	"https://feeds.npr.org/1004/rss.xml",

	// The Guardian
	"https://www.theguardian.com/world/rss",
	"https://www.theguardian.com/politics/rss",
	"https://www.theguardian.com/us-news/rss",

	// Foreign Policy
	"https://foreignpolicy.com/feed/",

	// Council on Foreign Relations
	"https://www.cfr.org/rss/region/all",

	// Politico
	"https://rss.politico.com/politics-news.xml",
	"https://rss.politico.com/congress.xml",

	// The Hill
	"https://thehill.com/homenews/feed/",
	"https://thehill.com/policy/defense/feed/",

	// Roll Call
	"https://rollcall.com/feed/",

	// South China Morning Post
	"https://www.scmp.com/rss/91/feed",
	"https://www.scmp.com/rss/2/feed",

	// India - The Hindu
	"https://www.thehindu.com/news/international/?service=rss",

	// Middle East Eye
	"https://www.middleeasteye.net/rss",

	// Euronews
	"https://feeds.feedburner.com/euronews/en/home/",

	// France 24
	"https://www.france24.com/en/rss",

	// NHK World
	"https://www3.nhk.or.jp/nhkworld/en/news/feeds/",

	// Voice of America
	"https://www.voanews.com/api/zmoyhrgkm",

	// Radio Free Europe
	"https://www.rferl.org/api/epiqq",

	// Brookings Institution
	"https://www.brookings.edu/feed/",

	// RAND Corporation
	"https://www.rand.org/pubs/periodicals/rss.xml",

	// Carnegie Endowment
	"https://carnegieendowment.org/rss/",

	// Chatham House
	"https://www.chathamhouse.org/rss.xml",

	// International Crisis Group
	"https://www.crisisgroup.org/rss.xml",

	// Arms Control Association
	"https://www.armscontrol.org/rss.xml",

	// DefenseOne
	"https://www.defenseone.com/rss/all/",

	// Breaking Defense
	"https://breakingdefense.com/feed/",
}
